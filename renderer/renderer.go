package renderer

import (
	"github.com/enriquebris/goconcurrentqueue"
	"github.com/g3n/engine/core"
	"github.com/g3n/engine/geometry"
	"github.com/g3n/engine/graphic"
	"github.com/g3n/engine/light"

	"github.com/g3n/engine/material"
	"github.com/g3n/engine/math32"
	"github.com/g3n/engine/util/application"
	"github.com/g3n/engine/util/logger"

	"github.com/vshashi01/webg3n/encode"
)

//AppSingleton used to the http calls
var AppSingleton *RenderingApp

// ImageSettings for rendering image
type ImageSettings struct {
	saturation   float64
	contrast     float64
	brightness   float64
	blur         float64
	pixelation   float64
	invert       bool
	quality      Quality
	isNavigating bool
}

// getJpegQuality returns quality depending on navigation movement
func (i *ImageSettings) getJpegQuality() int {
	if i.isNavigating {
		return i.quality.jpegQualityNav
	} else {
		return i.quality.jpegQualityStill
	}
}

// getPixelation returns pixelation depending on navigation movement
// A global pixelation level will override preset pixelation levels
func (i *ImageSettings) getPixelation() float64 {
	if i.pixelation > 1.0 {
		return i.pixelation
	}
	if i.isNavigating {
		return i.quality.pixelationNav
	} else {
		return i.quality.pixelationStill
	}
}

// Quality Image quality settings for still and navigating situations
type Quality struct {
	jpegQualityStill int
	jpegQualityNav   int
	pixelationStill  float64
	pixelationNav    float64
}

// high image quality definition
var highQ Quality = Quality{jpegQualityStill: 100, jpegQualityNav: 90, pixelationStill: 1.0, pixelationNav: 1.0}

// medium image quality definition
var mediumQ Quality = Quality{jpegQualityStill: 80, jpegQualityNav: 60, pixelationStill: 1.0, pixelationNav: 1.2}

// low image quality definition
var lowQ Quality = Quality{jpegQualityStill: 60, jpegQualityNav: 40, pixelationStill: 1.0, pixelationNav: 1.5}

// RenderingApp application settings
type RenderingApp struct {
	application.Application
	x, y, z           float32
	cImagestream      chan []byte
	cCommands         chan []byte
	Width             int
	Height            int
	imageSettings     ImageSettings
	selectionBuffer   map[core.INode][]graphic.GraphicMaterial
	selectionMaterial material.IMaterial
	modelpath         string
	entityList        map[string]*core.Node
	graphicList       map[*core.Node]*graphic.Mesh
	Debug             bool
	frameQueue        *goconcurrentqueue.FIFO

	//nodeBuffer        map[string]*core.Node
}

// LoadRenderingApp loads the rendering application
func LoadRenderingApp(app *RenderingApp, sessionID string, h int, w int, write chan []byte, read chan []byte, modelpath string) {
	a, err := application.Create(application.Options{
		Title:       "g3nServerApplication",
		Width:       w,
		Height:      h,
		Fullscreen:  false,
		LogPrefix:   sessionID,
		LogLevel:    logger.DEBUG,
		TargetFPS:   30,
		EnableFlags: true,
	})

	if err != nil {
		panic(err)
	}

	app.Application = *a
	app.Width = w
	app.Height = h

	app.imageSettings = ImageSettings{
		saturation: 0,
		brightness: 0,
		contrast:   0,
		blur:       0,
		pixelation: 1.0,
		invert:     false,
		quality:    highQ,
	}

	app.cImagestream = write
	app.cCommands = read
	app.modelpath = modelpath

	// create the queue and assign an initial frame to it
	app.frameQueue = goconcurrentqueue.NewFIFO()
	firstFrame := encode.NewInitialFrameContainer(w, h)
	app.frameQueue.Enqueue(firstFrame)

	AppSingleton = app
	app.setupScene()
	app.Application.Subscribe(application.OnAfterRender, app.onRender)

	// run the gstreamer pipeline on a separate goroutine
	go encode.RunPhantom3DPipeline(app.frameQueue, app.Width, app.Height)

	// run the command loop on a separate goroutine
	go app.commandLoop()
	err = app.Run()
	if err != nil {
		panic(err)
	}

	app.Log().Info("app was running for %f seconds\n", app.RunSeconds())
}

// setupScene sets up the current scene
func (app *RenderingApp) setupScene() {
	app.selectionMaterial = material.NewPhong(math32.NewColor("gold"))
	app.selectionBuffer = make(map[core.INode][]graphic.GraphicMaterial)
	//app.nodeBuffer = make(map[string]*core.Node)
	app.entityList = make(map[string]*core.Node)
	app.graphicList = make(map[*core.Node]*graphic.Mesh)

	app.Gl().ClearColor(0.2, 0.2, 0.2, 1.0)

	axes := graphic.NewAxisHelper(50.0)
	app.Scene().Add(axes)

	amb := light.NewAmbient(math32.NewColor("white"), 1.0)
	app.Scene().Add(amb)

	plight := light.NewPoint(math32.NewColor("white"), 40)
	plight.SetPosition(100, 20, 70)
	plight.SetLinearDecay(.001)
	plight.SetQuadraticDecay(.001)
	app.Scene().Add(plight)

	p := math32.Vector3{X: 0, Y: 0, Z: 0}

	dirLight := light.NewDirectional(math32.NewColor("white"), 1.0)
	dirLight.SetDirectionVec(&p)
	app.Scene().Add(dirLight)

	app.Camera().GetCamera().SetPosition(12, 1, 5)

	app.Camera().GetCamera().LookAt(&p)
	app.CameraPersp().SetFov(60)

	app.loadDefaultBlueBox()
	app.loadDefaultRedBox()

	app.zoomToExtent()
	app.Orbit().Enabled = true

}

func (app *RenderingApp) loadDefaultBlueBox() {
	name := "BlueBox"
	box := geometry.NewBox(25.0, 25.0, 25.0)
	boxmat := material.NewStandard(math32.NewColor("blue"))
	boxmesh := graphic.NewMesh(box, boxmat)

	app.LoadMeshEntity(boxmesh, name)
}

func (app *RenderingApp) loadDefaultRedBox() {
	name := "RedBox"
	box := geometry.NewPositionedBox(25.0, 25.0, 25.0, 50.0, 50.0, 50.0)
	mat := material.NewStandard(math32.NewColor("firebrick"))
	mesh := graphic.NewMesh(box, mat)

	app.LoadMeshEntity(mesh, name)
}

//LoadMeshEntity will load the Mesh entity properly to the scene with the Buffer updated, checks name as well
func (app *RenderingApp) LoadMeshEntity(mesh *graphic.Mesh, name string) bool {

	_, exist := app.entityList[name]

	if exist {
		app.SendMessageToClient("LoadModel", "Entity with same name already exist")
		return false
	}

	mesh.SetName(name)
	app.Scene().Add(mesh)

	for _, node := range app.Scene().Children() {
		ptr := node.GetNode()

		if ptr.Name() == name {
			app.entityList[name] = ptr
			app.graphicList[ptr] = mesh
		}
	}

	return true
}

// func returnGraphic(inode core.INode) (*graphic.IGraphic, bool) {
// 	gnode, ok := inode.(graphic.IGraphic)

// 	if ok {
// 		return &gnode, ok
// 	}

// }

//GetEntityList returns map
func (app *RenderingApp) GetEntityList() map[string]*core.Node {
	return app.entityList
}
