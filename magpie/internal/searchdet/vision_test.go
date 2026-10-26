package searchdet

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"strings"
	"testing"
)

// jpegWithEXIF is a real JPEG with an APP1 segment carrying GPS text, the
// shape a phone photo has before it leaves the device.
func jpegWithEXIF(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 12))
	for y := 0; y < 12; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 16), G: uint8(y * 20), B: 90, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	payload := append([]byte("Exif\x00\x00"), []byte("GPSLatitude=39.9042;GPSLongitude=116.4074;Make=MolaCam")...)
	segLen := len(payload) + 2
	out := make([]byte, 0, len(raw)+4+len(payload))
	out = append(out, raw[:2]...)
	out = append(out, 0xFF, 0xE1, byte(segLen>>8), byte(segLen))
	out = append(out, payload...)
	out = append(out, raw[2:]...)
	return out
}

func TestStripImageMetadataRemovesEXIF(t *testing.T) {
	raw := jpegWithEXIF(t)
	if !bytes.Contains(raw, []byte("GPSLatitude")) {
		t.Fatal("fixture has no EXIF marker")
	}
	clean, format, err := stripImageMetadata(raw)
	if err != nil {
		t.Fatalf("strip: %v", err)
	}
	if format != "jpeg" {
		t.Fatalf("format = %q, want jpeg", format)
	}
	for _, marker := range []string{"Exif", "GPSLatitude", "MolaCam"} {
		if bytes.Contains(clean, []byte(marker)) {
			t.Fatalf("%q survived the strip", marker)
		}
	}
	img, decoded, err := image.Decode(bytes.NewReader(clean))
	if err != nil {
		t.Fatalf("stripped image does not decode: %v", err)
	}
	if decoded != "jpeg" || img.Bounds().Dx() != 16 || img.Bounds().Dy() != 12 {
		t.Fatalf("stripped image changed: %s %v", decoded, img.Bounds())
	}
}

// TestVisionSendsNoEXIF checks the guarantee at the call site, not only the
// helper: the data URI handed to the HTTP layer is the re-encoded pixels.
func TestVisionSendsNoEXIF(t *testing.T) {
	old := visionPost
	defer func() { visionPost = old }()
	var sent string
	visionPost = func(_ context.Context, _ string, _ map[string]string, in, out any) error {
		req := in.(map[string]any)
		msgs := req["messages"].([]any)
		content := msgs[0].(map[string]any)["content"].([]any)
		sent = content[1].(map[string]any)["image_url"].(map[string]string)["url"]
		r := out.(*visionReply)
		r.Choices = []visionChoice{{Message: visionMessage{Content: "一张测试图片"}}}
		return nil
	}
	desc, err := visionDescribe(context.Background(), jpegWithEXIF(t), Keys{
		VisionAPIBase: "http://vision.test/v1",
		VisionAPIKey:  "test-key",
	})
	if err != nil {
		t.Fatalf("visionDescribe: %v", err)
	}
	if desc != "一张测试图片" {
		t.Fatalf("description = %q", desc)
	}
	if !strings.HasPrefix(sent, "data:image/jpeg;base64,") {
		t.Fatalf("unexpected data URI: %.40s", sent)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(sent, "data:image/jpeg;base64,"))
	if err != nil {
		t.Fatalf("data URI is not base64: %v", err)
	}
	for _, marker := range []string{"Exif", "GPSLatitude", "MolaCam"} {
		if bytes.Contains(raw, []byte(marker)) {
			t.Fatalf("%q was sent to the vision API", marker)
		}
	}
}
