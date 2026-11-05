package browsebridge

import (
	"bytes"
	"embed"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strings"
	"sync"
)

// Google Material Icons, Apache-2.0. Source PNGs are unmodified and pinned in
// assets/sources.json. Only their tint is adapted; no icon geometry is drawn.
//
//go:embed assets/*.png
var iconAssets embed.FS

const iconRevision = "material-10ecc147"
const iconSize = 96

var iconsOnce sync.Once
var icons = map[string][]byte{}

func iconPNG(kind string) []byte {
	iconsOnce.Do(func() {
		for _, name := range []string{"albums", "tracks", "artists", "playlists"} {
			data, err := iconAssets.ReadFile("assets/" + name + ".png")
			if err != nil {
				panic(err)
			}
			source, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				panic(err)
			}
			outputImage := image.NewNRGBA(source.Bounds())
			for y := source.Bounds().Min.Y; y < source.Bounds().Max.Y; y++ {
				for x := source.Bounds().Min.X; x < source.Bounds().Max.X; x++ {
					_, _, _, a := source.At(x, y).RGBA()
					outputImage.SetNRGBA(x, y, color.NRGBA{R: 59, G: 130, B: 246, A: uint8(a >> 8)})
				}
			}
			var output bytes.Buffer
			if err = png.Encode(&output, outputImage); err != nil {
				panic(err)
			}
			icons[name] = output.Bytes()
		}
	})
	return icons[kind]
}
func (b *Bridge) icon(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSuffix(r.PathValue("name"), ".png")
	data := iconPNG(name)
	if len(data) == 0 || r.PathValue("name") != name+".png" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(data)
}
