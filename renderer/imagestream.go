package renderer

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"image"
	"image/jpeg"

	"github.com/disintegration/imaging"
	"github.com/g3n/engine/gls"
	"github.com/vshashi01/webg3n/encode"
)

// onRender event handler for onRender event
func (app *RenderingApp) onRender(evname string, ev interface{}) {
	buffer := app.makeScreenShot()

	newCont := encode.NewGstreamerFrameContainer(buffer, uint(app.FrameCount()), false)
	app.frameQueue.Enqueue(newCont)
}

var md5SumBuffer [16]byte

// makeScreenShot reads the opengl buffer, encodes it as jpeg and sends it to the channel
func (app *RenderingApp) makeScreenShot() []byte {
	w := app.Width
	h := app.Height
	data := app.Gl().ReadPixels(0, 0, w, h, gls.RGBA, gls.UNSIGNED_BYTE)
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	img.Pix = data

	if app.imageSettings.getPixelation() > 1.0 {
		img = imaging.Fit(img, int(float64(w)/app.imageSettings.getPixelation()), int(float64(h)/app.imageSettings.getPixelation()), imaging.NearestNeighbor)
	}
	if app.imageSettings.brightness != 0 {
		img = imaging.AdjustBrightness(img, app.imageSettings.brightness)
	}
	if app.imageSettings.contrast != 0 {
		img = imaging.AdjustContrast(img, app.imageSettings.contrast)
	}
	if app.imageSettings.saturation != 0 {
		img = imaging.AdjustSaturation(img, app.imageSettings.saturation)
	}
	if app.imageSettings.blur != 0 {
		img = imaging.Blur(img, app.imageSettings.blur)
	}
	if app.imageSettings.invert {
		img = imaging.Invert(img)
	}

	img = imaging.FlipV(img)
	buf := new(bytes.Buffer)

	if app.Debug {
		img = DrawByteGraph(img)
	}

	var opt jpeg.Options
	opt.Quality = app.imageSettings.getJpegQuality()
	jpeg.Encode(buf, img, &opt)
	imageBit := buf.Bytes()

	// get md5 checksum from image to check if image changed
	// only send a new image to the client if there has been any change.
	md := md5.Sum(imageBit)
	if md5SumBuffer != md {
		imgBase64Str := base64.StdEncoding.EncodeToString([]byte(imageBit))
		if app.Debug {
			AddToByteBuffer(len(imgBase64Str))
		}
		app.cImagestream <- []byte(imgBase64Str)
	}
	md5SumBuffer = md

	return img.Pix
}
