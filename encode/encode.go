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

const width = 620
const height = 480

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
func RunPhantom3DPipeline(frameQueue *goconcurrentqueue.FIFO, width, height int) {
	runGMainLoop(func() error {
		var pipeline *gst.Pipeline
		var err error
		if pipeline, _, err = createPhantom3DPipeline(frameQueue, uint(width), uint(height)); err != nil {
			return err
		}
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

func createPhantom3DPipeline(frameQueue *goconcurrentqueue.FIFO, width, height uint) (*gst.Pipeline, *app.Source, error) {
	gst.Init(nil)

	// Create a pipeline
	pipeline, err := gst.NewPipeline("")
	if err != nil {
		return nil, nil, err
	}

	// Create the elements
	elems, err := gst.NewElementMany("appsrc", "videoconvert", "autovideosink")
	if err != nil {
		return nil, nil, err
	}

	// Add the elements to the pipeline and link them
	pipeline.AddMany(elems...)
	gst.ElementLinkMany(elems...)

	// Get the app sourrce from the first element returned
	src := app.SrcFromElement(elems[0])

	// Specify the format we want to provide as application into the pipeline
	// by creating a video info with the given format and creating caps from it for the appsrc element.
	videoInfo := video.NewInfo().
		WithFormat(video.FormatRGBA, width, height).
		WithFPS(gst.Fraction(30, 1))

	src.SetCaps(videoInfo.ToCaps())
	src.SetProperty("format", gst.FormatTime)

	previousTime := time.Now()
	previousFrame := NewInitialFrameContainer(int(width), int(height))
	frameCount := 0

	// Since our appsrc element operates in pull mode (it asks us to provide data),
	// we add a handler for the need-data callback and provide new data from there.
	// In our case, we told gstreamer that we do 2 frames per second. While the
	// buffers of all elements of the pipeline are still empty, this will be called
	// a couple of times until all of them are filled. After this initial period,
	// this handler will be called (on average) as many times as the FPS set.
	src.SetCallbacks(&app.SourceCallbacks{
		NeedDataFunc: func(self *app.Source, _ uint) {
			fmt.Println("Since last time:", time.Since(previousTime))
			previousTime = time.Now()

			var buffer []byte
			var currentFrame uint
			var isLastFrame bool
			var newFrame *GStreamerFrameContainer
			frameInt, queueErr := frameQueue.Dequeue()

			if queueErr != nil {
				fmt.Println("Queue is locked or empty")
			}

			newFrame, isExpectedType := frameInt.(*GStreamerFrameContainer)

			if !isExpectedType {
				fmt.Println("Wrong type passed! Could be nil Passing the backup frame instead")
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
			previousTime = time.Now()

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

// // UpdateBuffer updates the pixels
// func UpdateDummyBuffer(imagestruct *FrameContainer, interval time.Duration, width, height int) {

// 	for {
// 		imagestruct.mutex.Lock()

// 		// Get all 256 colors in the RGB8P palette.
// 		palette := video.FormatRGB8P.Palette()
// 		pixels := produceImageFrame(palette[imagestruct.nextFrameCount], width, height)

// 		imagestruct.buffer = pixels

// 		imagestruct.nextFrameCount++

// 		imagestruct.mutex.Unlock()

// 		//fmt.Println("Updated Buffer")

// 		time.Sleep(interval)
// 	}

// }

// // RunPhantom3DPipeline is called as a goroutine to run the pipeline
// func RunDummyPhantom3DPipeline(frameCont *FrameContainer, width, height int) {
// 	runGMainLoop(func() error {
// 		var pipeline *gst.Pipeline
// 		var err error
// 		if pipeline, _, err = createDummyPhantom3DPipeline(frameCont, uint(width), uint(height)); err != nil {
// 			return err
// 		}
// 		return activityLoop(pipeline)
// 	})
// }

// func createDummyPhantom3DPipeline(imagestruct *FrameContainer, width, height uint) (*gst.Pipeline, *app.Source, error) {
// 	gst.Init(nil)

// 	// Create a pipeline
// 	pipeline, err := gst.NewPipeline("")
// 	if err != nil {
// 		return nil, nil, err
// 	}

// 	// Create the elements
// 	elems, err := gst.NewElementMany("appsrc", "videoconvert", "autovideosink")
// 	if err != nil {
// 		return nil, nil, err
// 	}

// 	// Add the elements to the pipeline and link them
// 	pipeline.AddMany(elems...)
// 	gst.ElementLinkMany(elems...)

// 	// Get the app sourrce from the first element returned
// 	src := app.SrcFromElement(elems[0])

// 	// Specify the format we want to provide as application into the pipeline
// 	// by creating a video info with the given format and creating caps from it for the appsrc element.
// 	videoInfo := video.NewInfo().
// 		WithFormat(video.FormatRGBA, width, height).
// 		WithFPS(gst.Fraction(10, 1))

// 	src.SetCaps(videoInfo.ToCaps())
// 	src.SetProperty("format", gst.FormatTime)
// 	src.SetProperty("do-timestamp", true)
// 	src.SetProperty("min-latency", 0)
// 	src.SetProperty("emit-signals", false)
// 	src.SetProperty("is-live", true)

// 	previousTime := time.Now()

// 	// Since our appsrc element operates in pull mode (it asks us to provide data),
// 	// we add a handler for the need-data callback and provide new data from there.
// 	// While the buffers of all elements of the pipeline are still empty, this will be called
// 	// a couple of times until all of them are filled. After this initial period,
// 	// this handler will be called (on average) as many times as the FPS set.
// 	src.SetCallbacks(&app.SourceCallbacks{
// 		NeedDataFunc: func(self *app.Source, _ uint) {
// 			fmt.Println("Pipeline callback Time since last time: ", time.Since(previousTime))
// 			previousTime = time.Now()

// 			imagestruct.mutex.RLock()
// 			currentFrame := imagestruct.nextFrameCount - 1
// 			//buffer := imagestruct.buffer
// 			imagestruct.mutex.RUnlock()
// 			fmt.Println("Time to process the read mutex: ", time.Since(previousTime))
// 			previousTime = time.Now()

// 			// If we've reached the end of the palette, end the stream.
// 			if currentFrame > 255 {
// 				src.EndStream()
// 				return
// 			}

// 			//fmt.Println("Producing frame:", imagestruct.nextFrameCount-1)

// 			// Create a buffer that can hold exactly one video RGBA frame.
// 			buf := gst.NewBufferWithSize(videoInfo.Size())
// 			//buf := gst.NewBufferFromBytes(buffer)
// 			fmt.Println("Time to create the GST buffer: ", time.Since(previousTime))
// 			previousTime = time.Now()

// 			// For each frame we produce, we set the timestamp when it should be displayed
// 			// The autovideosink will use this information to display the frame at the right time.
// 			buf.SetPresentationTimestamp(time.Duration(currentFrame) * 100 * time.Millisecond)

// 			mapInfo := buf.Map(gst.MapWrite)

// 			if mapInfo == nil {
// 				fmt.Println("Something went wrong with MapInfo")
// 				return
// 			}

// 			//mapInfo.CopyData(buffer)

// 			//buf.Unmap(mapInfo)

// 			fmt.Println("Time to write buffer and timestamp: ", time.Since(previousTime))
// 			previousTime = time.Now()

// 			// Push the buffer onto the pipeline.
// 			self.PushBuffer(buf)
// 			fmt.Println("Time to push buffer: ", time.Since(previousTime))
// 			previousTime = time.Now()

// 			//fmt.Println("Time to process this frame: ", time.Since(previousTime))
// 			//previousTime = time.Now()

// 			//i++
// 		},
// 	})

// 	return pipeline, src, nil
// }
