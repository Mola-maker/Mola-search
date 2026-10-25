package searchdet

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"strings"
	"testing"
)

// TestPrepareWatchQueryMasksPersonalData: a saved watch is a search that
// runs unattended, so its query passes the same gate as an investigate call.
func TestPrepareWatchQueryMasksPersonalData(t *testing.T) {
	got, err := PrepareWatchQuery("联系 " + testPhone + " 查北京冰雹理赔")
	if err != nil {
		t.Fatalf("clean query refused: %v", err)
	}
	if strings.Contains(got, testPhone) {
		t.Fatalf("the phone number would be stored and searched: %q", got)
	}
	if !strings.Contains(got, "{{PHONE_") {
		t.Fatalf("the query was not masked: %q", got)
	}
}

func TestPrepareWatchQueryRefusesRedline(t *testing.T) {
	if _, err := PrepareWatchQuery("帮我人肉开盒查某人"); err == nil {
		t.Fatal("a red-line watch query was accepted")
	}
}

// TestSanitizeWatchMasksStoredFields: what a watch keeps is shown again on
// every page load, so every field the page renders is masked.
func TestSanitizeWatchMasksStoredFields(t *testing.T) {
	job := SanitizeWatch(StakeoutJob{
		Title: "关注 " + testPhone, Query: "查 " + testEmail, SearchParams: "渠道 " + testPhone,
		StatusBadge: testEmail, LatestDiff: "新增 " + testPhone, SourceURL: "https://x.test/?u=" + testEmail,
		TopDomains: "https://a.test/?u=" + testPhone + "|https://b.test/",
	})
	for _, field := range []string{job.Title, job.Query, job.SearchParams, job.StatusBadge, job.LatestDiff, job.SourceURL, job.TopDomains} {
		if strings.Contains(field, testPhone) || strings.Contains(field, testEmail) {
			t.Fatalf("a stored watch field kept personal data: %q", field)
		}
	}
}

// jpegWithGPS is a real JPEG whose APP1 is a real little-endian TIFF block:
// IFD0 names the camera and points at a GPS IFD carrying 39.9042 N,
// 116.4074 E. jpegWithEXIF in vision_test.go is a text payload for the
// strip test; this one is structured so parseEXIF reads it.
func jpegWithGPS(t *testing.T) []byte {
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

	le := binary.LittleEndian
	tiff := []byte{'I', 'I'}
	tiff = le.AppendUint16(tiff, 42)
	tiff = le.AppendUint32(tiff, 8) // IFD0 at 8
	entry := func(tag, typ uint16, count, val uint32) {
		tiff = le.AppendUint16(tiff, tag)
		tiff = le.AppendUint16(tiff, typ)
		tiff = le.AppendUint32(tiff, count)
		tiff = le.AppendUint32(tiff, val)
	}
	// IFD0: Make (ASCII, at 140) and the GPS IFD pointer (38).
	tiff = le.AppendUint16(tiff, 2)
	entry(0x010F, 2, 8, 140)
	entry(0x8825, 4, 1, 38)
	tiff = le.AppendUint32(tiff, 0) // no next IFD
	// GPS IFD at 38: N/E refs inline, coordinates at 92 and 116.
	tiff = le.AppendUint16(tiff, 4)
	entry(1, 2, 2, uint32('N'))
	entry(2, 5, 3, 92)
	entry(3, 2, 2, uint32('E'))
	entry(4, 5, 3, 116)
	tiff = le.AppendUint32(tiff, 0)
	for _, r := range [][2]uint32{{39, 1}, {54, 1}, {1512, 100}, {116, 1}, {24, 1}, {2664, 100}} {
		tiff = le.AppendUint32(tiff, r[0])
		tiff = le.AppendUint32(tiff, r[1])
	}
	tiff = append(tiff, []byte("MolaCam\x00")...) // at 140

	payload := append([]byte("Exif\x00\x00"), tiff...)
	segLen := len(payload) + 2
	out := make([]byte, 0, len(raw)+4+len(payload))
	out = append(out, raw[:2]...)
	out = append(out, 0xFF, 0xE1, byte(segLen>>8), byte(segLen))
	out = append(out, payload...)
	out = append(out, raw[2:]...)
	return out
}

// TestImageCardHidesGPSCoordinates: the upload card is returned outside the
// investigation's output gate, so the card itself must never print where a
// phone photo was taken.
func TestImageCardHidesGPSCoordinates(t *testing.T) {
	card, err := ImageCard("photo.jpg", jpegWithGPS(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, coord := range []string{"39.9042", "116.4074"} {
		if strings.Contains(card.Summary, coord) {
			t.Fatalf("GPS coordinate %s shown in the card: %s", coord, card.Summary)
		}
	}
	if !strings.Contains(card.Summary, "GPS 存在") {
		t.Fatalf("the card does not say the tag exists: %s", card.Summary)
	}
}
