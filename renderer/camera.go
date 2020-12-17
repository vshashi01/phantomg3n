package renderer

import (
	"log"

	"github.com/g3n/engine/math32"
)

// getCenter gets the centerpoint of a 3D box
func getCenter(box math32.Box3) *math32.Vector3 {
	return box.Center(nil)
}

// focusOnSelection sets the camera focus on the current selection
func (app *RenderingApp) focusOnSelection() {
	var bbox *math32.Box3
	first := true
	for inode := range app.selectionBuffer {
		tmp := inode.BoundingBox()
		if first {
			bbox = math32.NewBox3(&tmp.Min, &tmp.Max)
			log.Println(bbox)
			first = false
		} else {
			bbox.ExpandByPoint(&tmp.Min)
			bbox.ExpandByPoint(&tmp.Max)
		}
	}
	if first {
		return
	}
	position := app.Camera().GetCamera().Position()
	C := bbox.Center(nil)
	r := C.DistanceTo(&bbox.Max)
	a := app.CameraPersp().Fov()
	d := r / math32.Sin(a/2)
	P := math32.Vector3{X: C.X, Y: C.Y, Z: C.Z}
	dir := math32.Vector3{X: C.X, Y: C.Y, Z: C.Z}
	P.Add(((position.Sub(C)).Normalize().MultiplyScalar(d)))
	dir.Sub(&P)
	app.Camera().GetCamera().SetPositionVec(&P)
	app.Camera().GetCamera().LookAt(C)
}

// getViewVectorByName gets a view direction vector by name
func getViewVectorByName(view string) math32.Vector3 {
	modifier := math32.Vector3{X: 0, Y: 0, Z: 0}
	switch view {
	case "top":
		modifier.Y = 10
	case "bottom":
		modifier.Y = -10
	case "front":
		modifier.Z = 10
	case "rear":
		modifier.Z = -10
	case "left":
		modifier.X = 10
	case "right":
		modifier.X = -10
	}
	return modifier
}

// setCamera set camera sets a camera standard view by name
func (app *RenderingApp) setCamera(view string) {
	modifier := getViewVectorByName(view)
	bbox := app.Scene().ChildAt(0).BoundingBox()
	C := bbox.Center(nil)
	pos := modifier.Add(C)
	app.focusCameraToCenter(*pos)
}

// focusCameraToCenter sets the camera focus to the center of the entire scene
func (app *RenderingApp) focusCameraToCenter(position math32.Vector3) {
	//bbox := app.Scene().ChildAt(0).BoundingBox()
	scenebbox := app.Scene().BoundingBox()
	C := scenebbox.Center(nil)
	r := C.DistanceTo(&scenebbox.Max)
	a := app.CameraPersp().Fov() * 0.6
	d := r / math32.Sin(a/2)
	P := math32.Vector3{X: C.X, Y: C.Y, Z: C.Z}
	dir := math32.Vector3{X: C.X, Y: C.Y, Z: C.Z}
	P.Add(((position.Sub(C)).Normalize().MultiplyScalar(d)))
	dir.Sub(&P)
	app.Camera().GetCamera().SetPositionVec(&dir)
	app.Camera().GetCamera().LookAt(C)
}

// zoomToExtent zooms the view to extent
func (app *RenderingApp) zoomToExtent() {
	pos := app.Camera().GetCamera().Position()

	// scenebbox := app.Scene().BoundingBox()

	// camerabbox := app.Camera().GetCamera().BoundingBox()
	// containsCamera := scenebbox.ContainsBox(&camerabbox)
	app.focusCameraToCenter(pos)
}
