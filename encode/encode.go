package encode

import (
	"fmt"
	"image"
	"image/color"
	"time"

	"github.com/enriquebris/goconcurrentqueue"
	"github.com/tinyzimmer/go-gst/gst"
	"github.com/tinyzimmer/go-gst/gst/app"
	"github.com/tinyzimmer/go-gst/gst/video"
)

//PhantomStreamer is useless
type PhantomStreamer struct {
	pipeline  *gst.Pipeline
	appSource *app.Source
	videoInfo video.Info
}

// FrameContainerInterface is the general interface for all frame containers
type FrameContainerInterface interface {
	GetBuffer() []byte
	GetFrameCount() uint
	IsLastFrame() bool
	GetInterface() *FrameContainerInterface
}

// GStreamerFrameContainer is type that fulfils the FrameContainerInterface
type GStreamerFrameContainer struct {
	FrameContainerInterface
	Framebuffer []byte
	FrameCount  uint
	LastFrame   bool
}

//NewGstreamerFrameContainer creates a well defined new container
func NewGstreamerFrameContainer(buffer []byte, frameCount uint, isLastFrame bool) *GStreamerFrameContainer {
	frameContainer := new(GStreamerFrameContainer)
	frameContainer.Framebuffer = buffer
	frameContainer.FrameCount = frameCount
	frameContainer.LastFrame = isLastFrame

	return frameContainer
}

//NewInitialFrameContainer creates a completely new FrameContainer initialized with the initial conditions
func NewInitialFrameContainer(width, height int) *GStreamerFrameContainer {
	palette := video.FormatRGB8P.Palette()
	pixels := produceImageFrame(palette[0], width, height)
	cont := NewGstreamerFrameContainer(pixels, 0, false)

	return cont
}

//GetBuffer returns the framebuffer
func (cont *GStreamerFrameContainer) GetBuffer() []byte {
	return cont.Framebuffer
}

//GetFrameCount returns the framecount
func (cont *GStreamerFrameContainer) GetFrameCount() uint {
	return cont.FrameCount
}

//IsLastFrame returns the if this is the last frame
func (cont *GStreamerFrameContainer) IsLastFrame() bool {
	return cont.LastFrame
}

//GetInterface returns the expected interface
func (cont *GStreamerFrameContainer) GetInterface() *FrameContainerInterface {
	return &cont.FrameContainerInterface
}

// RunPhantom3DPipeline is called as a goroutine to run the pipeline
func RunPhantom3DPipeline(frameQueue *goconcurrentqueue.FIFO, port int, width, height int) {
	runGMainLoop(func() error {
		var pipeline *gst.Pipeline
		var err error
		if pipeline, _, err = createPhantom3DPipeline(frameQueue, port, uint(width), uint(height)); err != nil {
			return err
		}

		fmt.Println("Pipeline created. Running Pipeline..")

		return activityLoop(pipeline)
	})
}

// runGMainLoop is used to create and run the GST main loop.
func runGMainLoop(f func() error) {
	mainLoop := gst.NewMainLoop(gst.DefaultMainContext(), false)

	defer mainLoop.Unref()

	go func() {
		if err := f(); err != nil {
			fmt.Println("ERROR!", err)
		}
		mainLoop.Quit()
	}()

	mainLoop.Run()
}

func createPhantom3DPipeline(frameQueue *goconcurrentqueue.FIFO, port int, initialWidth, initialHeight uint) (*gst.Pipeline, *app.Source, error) {
	gst.Init(nil)

	// Create a pipeline
	pipeline, err := gst.NewPipeline("")
	if err != nil {
		return nil, nil, err
	}

	// Create the elements
	//elems, err := gst.NewElementMany("appsrc", "videoconvert", "autovideosink")
	elems, err := gst.NewElementMany("appsrc", "videoconvert", "vp8enc", "rtpvp8pay", "udpsink")
	if err != nil {
		return nil, nil, err
	}

	// Add the elements to the pipeline
	addError := pipeline.AddMany(elems...)

	if addError != nil {
		fmt.Println(addError)
		fmt.Println("Something went wrong with adding elements")
	}

	//Link all the elements in the pipeline
	linkError := gst.ElementLinkMany(elems...)

	if linkError != nil {
		fmt.Println(linkError)
		fmt.Println("Something went wrong with linking elements")
	}

	// Get the app sourrce from the first element returned
	src := app.SrcFromElement(elems[0])

	// Specify the format we want to provide as application into the pipeline
	// by creating a video info with the given format and creating caps from it for the appsrc element.
	videoInfo := video.NewInfo().
		WithFormat(video.FormatRGBA, initialWidth, initialHeight).
		WithFPS(gst.Fraction(30, 1))

	src.SetCaps(videoInfo.ToCaps())
	src.SetProperty("format", gst.FormatTime)

	vp8encElem := elems[2]
	vp8encElem.SetProperty("error-resilient", 2)
	vp8encElem.SetProperty("keyframe-max-dist", 10)
	vp8encElem.SetProperty("auto-alt-ref", true)
	vp8encElem.SetProperty("cpu-used", 1)
	vp8encElem.SetProperty("deadline", 1)

	udpsinkElem := elems[4]
	udpsinkElem.SetProperty("host", "127.0.0.1")
	udpsinkElem.SetProperty("port", port)

	//previousTime := time.Now()
	previousFrame := NewInitialFrameContainer(int(initialWidth), int(initialHeight))
	frameCount := 0

	// Since our appsrc element operates in pull mode (it asks us to provide data),
	// we add a handler for the need-data callback and provide new data from there.
	// In our case, we told gstreamer that we do 2 frames per second. While the
	// buffers of all elements of the pipeline are still empty, this will be called
	// a couple of times until all of them are filled. After this initial period,
	// this handler will be called (on average) as many times as the FPS set.
	src.SetCallbacks(&app.SourceCallbacks{
		NeedDataFunc: func(self *app.Source, _ uint) {
			//fmt.Println("Since last time:", time.Since(previousTime))
			//previousTime = time.Now()

			var buffer []byte
			var currentFrame uint
			var isLastFrame bool
			var newFrame *GStreamerFrameContainer
			frameInt, queueErr := frameQueue.Dequeue()

			if queueErr != nil {
				//fmt.Println(queueErr)
			}

			newFrame, isExpectedType := frameInt.(*GStreamerFrameContainer)

			if !isExpectedType {
				//fmt.Println("Wrong type passed! Could be nil Passing the backup frame instead")
				// if skipped a frame pump the last frame sent as a new frame just to ensure the video FPS never drops
				newFrame = previousFrame
			}

			//increment the frameCount and assign the new frame as the previous frame
			frameCount++
			previousFrame = newFrame

			buffer = newFrame.GetBuffer()
			currentFrame = uint(frameCount)
			isLastFrame = newFrame.IsLastFrame()

			// If we've reached the end of the palette, end the stream.
			if isLastFrame {
				src.EndStream()
				return
			}

			// Create a gst.Buffer with the desired frame
			buf := gst.NewBufferFromBytes(buffer)

			// For each frame we produce, we set the timestamp when it should be displayed
			// The autovideosink will use this information to display the frame at the right time.
			// Update the int value below to the appox milliseconds the timestamp shall be set to
			buf.SetPresentationTimestamp(time.Duration(currentFrame) * 33 * time.Millisecond)

			//mapping and writing to the buffer is not needed

			// Push the buffer onto the pipeline.
			self.PushBuffer(buf)

			// fmt.Println("Pushed frame ", currentFrame)
			// fmt.Println("Time to process this frame", time.Since(previousTime))
			//previousTime = time.Now()

			//i++
		},
	})

	return pipeline, src, nil
}

func produceImageFrame(c color.Color, width, height int) []uint8 {
	upLeft := image.Point{0, 0}
	lowRight := image.Point{width, height}
	img := image.NewRGBA(image.Rectangle{upLeft, lowRight})

	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			img.Set(x, y, c)
		}
	}

	return img.Pix
}

func handleMessage(msg *gst.Message) error {
	defer msg.Unref() // Messages are a good candidate for trying out runtime finalizers

	switch msg.Type() {
	case gst.MessageEOS:
		return app.ErrEOS
	case gst.MessageError:
		gerr := msg.ParseError()
		if debug := gerr.DebugString(); debug != "" {
			fmt.Println(debug)
		}
		return gerr
	}

	return nil
}

func activityLoop(pipeline *gst.Pipeline) error {

	defer pipeline.Destroy() // Will stop and unref the pipeline when this function returns

	// Start the pipeline
	pipeline.SetState(gst.StatePlaying)

	// Retrieve the bus from the pipeline
	bus := pipeline.GetPipelineBus()

	// Loop over messsages from the pipeline
	for {
		msg := bus.TimedPop(time.Duration(-1))
		if msg == nil {
			break
		}
		if err := handleMessage(msg); err != nil {
			return err
		}
	}

	return nil
}
