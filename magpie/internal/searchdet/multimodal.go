// The agency's five modality checks, each card built from a live request or a
// real decode: a place's current weather held against what the query claims,
// an image's perceptual fingerprints and EXIF, a Bilibili video's danmaku,
// and the accessibility tree of a page or an Android screen. A check that
// cannot run says so in its card — none of these invent a result.
package searchdet

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/yetone/magpie/internal/proc"
)

// ---------------------------------------------------------------------------
// M25: geography × live weather
// ---------------------------------------------------------------------------

// cnCities is the closed vocabulary the weather path recognises. A query
// outside it gets no card rather than a guessed location: geocoding a
// misread entity is worse than saying nothing.
var cnCities = []string{
	"北京", "上海", "广州", "深圳", "天津", "重庆", "成都", "杭州", "武汉", "西安",
	"南京", "苏州", "郑州", "长沙", "沈阳", "青岛", "大连", "宁波", "厦门", "福州",
	"济南", "合肥", "昆明", "哈尔滨", "长春", "石家庄", "太原", "南昌", "贵阳", "南宁",
	"海口", "兰州", "银川", "西宁", "乌鲁木齐", "呼和浩特", "拉萨", "香港", "澳门", "台北",
	"东莞", "佛山", "无锡", "温州", "珠海", "三亚", "洛阳", "烟台", "常州", "徐州",
	"唐山", "保定", "惠州", "中山", "嘉兴", "绍兴", "台州", "金华", "泉州", "漳州",
}

// landmarkCity maps a place people name instead of its city to that city.
// Longest match wins, so "朝阳公园" is not read as the city 朝阳.
var landmarkCity = map[string]string{
	"天安门": "北京", "故宫": "北京", "朝阳公园": "北京", "中关村": "北京", "国贸": "北京",
	"望京": "北京", "三里屯": "北京", "五道口": "北京", "颐和园": "北京", "鸟巢": "北京",
	"外滩": "上海", "陆家嘴": "上海", "迪士尼": "上海", "静安寺": "上海", "徐家汇": "上海",
	"人民广场": "上海", "东方明珠": "上海", "虹桥": "上海",
	"广州塔": "广州", "珠江新城": "广州", "深圳湾": "深圳", "华强北": "深圳", "世界之窗": "深圳",
	"西湖": "杭州", "灵隐寺": "杭州", "钱塘江": "杭州",
	"春熙路": "成都", "太古里": "成都", "宽窄巷子": "成都", "天府广场": "成都",
	"洪崖洞": "重庆", "解放碑": "重庆", "磁器口": "重庆",
	"光谷": "武汉", "黄鹤楼": "武汉", "江汉路": "武汉",
	"大雁塔": "西安", "兵马俑": "西安", "钟楼": "西安",
	"鼓浪屿": "厦门", "中山路": "厦门",
	"栈桥": "青岛", "五四广场": "青岛", "崂山": "青岛",
	"中山陵": "南京", "夫子庙": "南京", "新街口": "南京",
	"趵突泉": "济南", "大明湖": "济南",
	"橘子洲": "长沙", "岳麓山": "长沙",
	"丽江古城": "丽江", "洱海": "大理",
}

// geoPoint is a resolved place with real coordinates.
type geoPoint struct {
	Name     string
	Lat, Lon float64
}

// liveWeather is one current observation. WeatherCode is -1 when the vendor
// gives a description but no WMO code (Amap), and PrecipMM is -1 when the
// vendor gives no precipitation figure at all — 0 would claim "dry".
type liveWeather struct {
	Provider    string
	TempC       float64
	WeatherCode int
	WindKmh     float64
	PrecipMM    float64
	Desc        string
}

// cityInQuery finds the city and landmark a query names. It scans the whole
// query rather than a prefix, because Chinese queries put the place anywhere.
func cityInQuery(query string) (city, landmark string) {
	best := ""
	for lm, c := range landmarkCity {
		if strings.Contains(query, lm) && len([]rune(lm)) > len([]rune(best)) {
			best, city = lm, c
		}
	}
	if best != "" {
		landmark = best
	}
	// An explicitly named city outranks the landmark's hint ("上海外滩").
	explicit := ""
	for _, c := range cnCities {
		if strings.Contains(query, c) && len([]rune(c)) > len([]rune(explicit)) {
			explicit = c
		}
	}
	if explicit != "" {
		city = explicit
	}
	return city, landmark
}

// geocode turns a place into coordinates: Amap when the user has a key,
// otherwise Nominatim. A failed Amap call falls through to the keyless
// geocoder instead of failing the card.
func geocode(ctx context.Context, city string, k Keys) (geoPoint, error) {
	city = strings.TrimSpace(city)
	if city == "" {
		return geoPoint{}, errors.New("geocode: empty place")
	}
	var amapErr error
	if k.Amap != "" {
		if p, err := amapGeocode(ctx, city, k.Amap); err == nil {
			return p, nil
		} else {
			amapErr = err
		}
	}
	p, err := nominatimGeocode(ctx, city)
	if err != nil && amapErr != nil {
		return geoPoint{}, fmt.Errorf("高德地理编码失败（%v）；Nominatim 也失败（%v）", amapErr, err)
	}
	return p, err
}

// amapGeocode calls Amap's geocoding API and reads the "lon,lat" location.
func amapGeocode(ctx context.Context, city, key string) (geoPoint, error) {
	u := "https://restapi.amap.com/v3/geocode/geo?key=" + url.QueryEscape(key) +
		"&citylimit=true&address=" + url.QueryEscape(city)
	var out struct {
		Status   string `json:"status"`
		Info     string `json:"info"`
		Geocodes []struct {
			FormattedAddress string `json:"formatted_address"`
			Location         string `json:"location"`
		} `json:"geocodes"`
	}
	if err := fetchJSON(ctx, u, nil, &out); err != nil {
		return geoPoint{}, err
	}
	if out.Status != "1" {
		return geoPoint{}, fmt.Errorf("amap geocode status=%s info=%s", out.Status, out.Info)
	}
	if len(out.Geocodes) == 0 {
		return geoPoint{}, errors.New("amap geocode: no result")
	}
	lonS, latS, ok := strings.Cut(out.Geocodes[0].Location, ",")
	if !ok {
		return geoPoint{}, errors.New("amap geocode: bad location " + out.Geocodes[0].Location)
	}
	lon, lat := atof(lonS), atof(latS)
	if lat == 0 && lon == 0 {
		return geoPoint{}, errors.New("amap geocode: zero location")
	}
	name := strings.TrimSpace(out.Geocodes[0].FormattedAddress)
	if name == "" {
		name = city
	}
	return geoPoint{Name: name, Lat: lat, Lon: lon}, nil
}

// nominatimGeocode is the keyless path. OSM's usage policy requires a User-Agent
// that identifies the application; a browser UA gets the connection dropped
// (curl with Chrome's UA returns EOF, an app UA returns 200), so this call
// sends its own instead of fetchJSON's browser-shaped default.
func nominatimGeocode(ctx context.Context, city string) (geoPoint, error) {
	u := "https://nominatim.openstreetmap.org/search?format=json&limit=1&accept-language=zh&q=" +
		url.QueryEscape(city)
	req, err := httpNewRequest(ctx, http.MethodGet, u, "")
	if err != nil {
		return geoPoint{}, err
	}
	req.Header.Set("User-Agent", nominatimUA)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	resp, err := client.Do(req)
	if err != nil {
		return geoPoint{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return geoPoint{}, errors.New("nominatim: HTTP " + resp.Status)
	}
	var out []struct {
		Lat         string `json:"lat"`
		Lon         string `json:"lon"`
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&out); err != nil {
		return geoPoint{}, err
	}
	if len(out) == 0 {
		return geoPoint{}, errors.New("nominatim: no result for " + city)
	}
	p := geoPoint{Name: strings.TrimSpace(out[0].DisplayName), Lat: atof(out[0].Lat), Lon: atof(out[0].Lon)}
	if p.Name == "" {
		p.Name = city
	}
	if p.Lat == 0 && p.Lon == 0 {
		return geoPoint{}, errors.New("nominatim: zero coordinates")
	}
	return p, nil
}

// nominatimUA names this app to OSM, which asks for a way to contact whoever
// is calling it.
const nominatimUA = "MolaSearch/1.0 (searchdet weather check)"

// weatherAt is the public observation call; weatherObs carries the vendor's
// report time as well, which the card shows as provenance.
func weatherAt(ctx context.Context, p geoPoint, k Keys) (liveWeather, error) {
	w, _, err := weatherObs(ctx, p, k)
	return w, err
}

// weatherObs picks the keyed vendor when the user has one, else Open-Meteo.
// The keyed vendor is allowed to fail into the keyless one: a card from the
// free path is more useful than no card.
func weatherObs(ctx context.Context, p geoPoint, k Keys) (liveWeather, string, error) {
	if k.Amap != "" {
		if w, at, err := amapWeather(ctx, p, k.Amap); err == nil {
			return w, at, nil
		}
	}
	return openMeteoWeather(ctx, p)
}

// amapWeather reads Amap's base live observation. Base does not carry a WMO
// code or a precipitation amount, so both are marked unknown.
func amapWeather(ctx context.Context, p geoPoint, key string) (liveWeather, string, error) {
	u := fmt.Sprintf("https://restapi.amap.com/v3/weather/weatherInfo?key=%s&extensions=base&longitude=%.6f&latitude=%.6f",
		url.QueryEscape(key), p.Lon, p.Lat)
	var out struct {
		Status string `json:"status"`
		Info   string `json:"info"`
		Lives  []struct {
			Weather       string `json:"weather"`
			Temperature   string `json:"temperature"`
			WindPower     string `json:"windpower"`
			WindDirection string `json:"winddirection"`
			ReportTime    string `json:"reporttime"`
		} `json:"lives"`
	}
	if err := fetchJSON(ctx, u, nil, &out); err != nil {
		return liveWeather{}, "", err
	}
	if out.Status != "1" {
		return liveWeather{}, "", fmt.Errorf("amap weather status=%s info=%s", out.Status, out.Info)
	}
	if len(out.Lives) == 0 {
		return liveWeather{}, "", errors.New("amap weather: no live observation")
	}
	l := out.Lives[0]
	desc := strings.TrimSpace(l.Weather)
	if dir := strings.TrimSpace(l.WindDirection); dir != "" && desc != "" {
		desc += "（" + dir + "风）"
	}
	w := liveWeather{
		Provider:    "高德天气",
		TempC:       atof(l.Temperature),
		WeatherCode: -1,
		WindKmh:     beaufortKmh(l.WindPower),
		PrecipMM:    -1,
		Desc:        desc,
	}
	return w, strings.TrimSpace(l.ReportTime), nil
}

// openMeteoWeather reads Open-Meteo's current block, which is free and needs
// no key but does carry a WMO code and precipitation.
func openMeteoWeather(ctx context.Context, p geoPoint) (liveWeather, string, error) {
	u := fmt.Sprintf("https://api.open-meteo.com/v1/forecast?latitude=%.4f&longitude=%.4f&current=temperature_2m,weather_code,wind_speed_10m,precipitation&timezone=auto",
		p.Lat, p.Lon)
	var out struct {
		Current struct {
			Time        string  `json:"time"`
			Temperature float64 `json:"temperature_2m"`
			WeatherCode int     `json:"weather_code"`
			Wind        float64 `json:"wind_speed_10m"`
			Precip      float64 `json:"precipitation"`
		} `json:"current"`
	}
	if err := fetchJSON(ctx, u, nil, &out); err != nil {
		return liveWeather{}, "", err
	}
	w := liveWeather{
		Provider:    "Open-Meteo",
		TempC:       out.Current.Temperature,
		WeatherCode: out.Current.WeatherCode,
		WindKmh:     out.Current.Wind,
		PrecipMM:    out.Current.Precip,
		Desc:        wmoText(out.Current.WeatherCode),
	}
	return w, out.Current.Time, nil
}

// wmoText names a WMO 4677 weather code in Chinese.
func wmoText(code int) string {
	switch code {
	case 0:
		return "晴"
	case 1:
		return "大部晴朗"
	case 2:
		return "局部多云"
	case 3:
		return "阴"
	case 45, 48:
		return "雾"
	case 51, 53, 55:
		return "毛毛雨"
	case 56, 57:
		return "冻毛毛雨"
	case 61:
		return "小雨"
	case 63:
		return "中雨"
	case 65:
		return "大雨"
	case 66, 67:
		return "冻雨"
	case 71:
		return "小雪"
	case 73:
		return "中雪"
	case 75:
		return "大雪"
	case 77:
		return "米雪"
	case 80:
		return "小阵雨"
	case 81:
		return "阵雨"
	case 82:
		return "强阵雨"
	case 85:
		return "小阵雪"
	case 86:
		return "强阵雪"
	case 95:
		return "雷暴"
	case 96, 99:
		return "雷暴伴冰雹"
	}
	return "未知天气"
}

// beaufortKmh converts Amap's Beaufort wind power ("≤3", "3-4") to km/h by
// the scale's midpoint. Amap reports the scale, not a speed.
func beaufortKmh(s string) float64 {
	m := reFirstInt.FindString(s)
	if m == "" {
		return 0
	}
	n := atoiOr(m, 0)
	mid := []float64{0, 3, 9, 16, 24, 34, 44, 56, 68, 80, 93, 105, 118}
	if n >= 0 && n < len(mid) {
		return mid[n]
	}
	return 118
}

var reFirstInt = regexp.MustCompile(`\d+`)

// weatherClaimKind reads the severe-weather claim a query makes, if any.
func weatherClaimKind(query string) string {
	switch {
	case strings.Contains(query, "冰雹"):
		return "hail"
	case strings.Contains(query, "暴雨"):
		return "rainstorm"
	case strings.Contains(query, "暴雪"), strings.Contains(query, "大雪"):
		return "snowstorm"
	case strings.Contains(query, "台风"):
		return "typhoon"
	}
	return ""
}

// weatherMatchesClaim says whether a live observation supports the claim.
// The comparison is deliberately coarse: the card's job is to catch a claim
// contradicted by clear conditions, not to grade the observation.
func weatherMatchesClaim(kind string, w liveWeather) bool {
	d := w.Desc
	switch kind {
	case "hail":
		return w.WeatherCode == 96 || w.WeatherCode == 99 ||
			strings.Contains(d, "冰雹") || strings.Contains(d, "雷")
	case "rainstorm":
		return w.WeatherCode == 65 || w.WeatherCode == 82 ||
			strings.Contains(d, "暴雨") || strings.Contains(d, "大雨")
	case "snowstorm":
		return w.WeatherCode == 75 || w.WeatherCode == 86 ||
			strings.Contains(d, "暴雪") || strings.Contains(d, "大雪")
	case "typhoon":
		return w.WindKmh >= 62 || strings.Contains(d, "台风")
	}
	return true
}

// weatherCard is the M25 card: where the query points, what the weather is
// there now, and whether that contradicts the query's claim. ok is false
// only when the query names no place this package can recognise.
func weatherCard(ctx context.Context, query string, k Keys) (MultimodalCard, bool) {
	city, landmark := cityInQuery(query)
	if city == "" && landmark == "" {
		return MultimodalCard{}, false
	}
	place := city
	if place == "" {
		place = landmark
	}
	p, err := geocode(ctx, place, k)
	if err != nil {
		return warnCard("🌦️", place+" 实况天气", "地理编码",
			"地理编码失败，未取到实况："+err.Error(), ""), true
	}
	w, observed, err := weatherObs(ctx, p, k)
	if err != nil {
		return warnCard("🌦️", place+" 实况天气", "气象接口",
			fmt.Sprintf("已定位到 %s（%.4f,%.4f），但实况拉取失败：%v", p.Name, p.Lat, p.Lon, err), ""), true
	}

	title := place + " 实况天气"
	if landmark != "" && landmark != place {
		title = place + "·" + landmark + " 实况天气"
	}
	badge := "实况一致"
	if kind := weatherClaimKind(query); kind != "" && !weatherMatchesClaim(kind, w) {
		badge = "与网传冲突"
	}

	precip := "降水量未知（高德 base 接口不提供）"
	if w.PrecipMM >= 0 {
		precip = fmt.Sprintf("降水量 %.1f mm", w.PrecipMM)
	}
	code := "未提供"
	if w.WeatherCode >= 0 {
		code = itoa(w.WeatherCode)
	}
	summary := fmt.Sprintf("%s（%.4f, %.4f）：%s，%.1f°C，风速 %.1f km/h，%s。",
		p.Name, p.Lat, p.Lon, w.Desc, w.TempC, w.WindKmh, precip)
	if observed != "" {
		summary += "观测时间：" + observed + "。"
	}

	return MultimodalCard{
		Icon:       "🌦️",
		Title:      title,
		Provider:   w.Provider,
		Status:     "✅",
		Badge:      badge,
		Summary:    summary,
		TechDetail: fmt.Sprintf("lat=%.4f lon=%.4f provider=%s code=%s tempC=%.1f windKmh=%.1f precipMm=%.1f", p.Lat, p.Lon, w.Provider, code, w.TempC, w.WindKmh, w.PrecipMM),
		SourceURL:  mapURL(place, p, w.Provider),
	}, true
}

// mapURL is a link a person can open at the coordinates the card checked.
func mapURL(place string, p geoPoint, provider string) string {
	if provider == "高德天气" {
		return fmt.Sprintf("https://uri.amap.com/marker?position=%.6f,%.6f&name=%s",
			p.Lon, p.Lat, url.QueryEscape(place))
	}
	return fmt.Sprintf("https://www.openstreetmap.org/?mlat=%.4f&mlon=%.4f#map=12/%.4f/%.4f",
		p.Lat, p.Lon, p.Lat, p.Lon)
}

// warnCard is a check that ran but could not finish: the failure is the
// card's content, never a silent success.
func warnCard(icon, title, provider, summary, sourceURL string) MultimodalCard {
	return MultimodalCard{
		Icon:      icon,
		Title:     title,
		Provider:  provider,
		Status:    "⚠️",
		Badge:     "数据不可用",
		Summary:   summary,
		SourceURL: sourceURL,
	}
}

// ---------------------------------------------------------------------------
// M22/M26: image fingerprints and EXIF
// ---------------------------------------------------------------------------

// imageCardFromBytes decodes the uploaded bytes for real, then reports the
// perceptual hashes, pixel statistics and whatever EXIF the JPEG carries.
// It takes no context because the engine's call site passes only bytes; the
// optional vision call therefore resolves its own keys and deadline here.
func imageCardFromBytes(name string, raw []byte) (MultimodalCard, error) {
	if len(raw) == 0 {
		return MultimodalCard{}, errors.New("image: empty bytes")
	}
	img, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return MultimodalCard{}, fmt.Errorf("image: decode: %w", err)
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return MultimodalCard{}, errors.New("image: zero dimensions")
	}

	dh := dHash64(img)
	ah := aHash64(img)
	avgR, avgG, avgB, dominant, dominantPct, nonFlat := imageStats(img)
	ex := parseEXIF(raw)

	base := strings.TrimSpace(name)
	if base == "" {
		base = "上传图片"
	} else {
		base = filepath.Base(base)
	}

	exifPart := "未发现 EXIF"
	if ex.any() {
		var bits []string
		cam := strings.TrimSpace(strings.Join([]string{ex.Make, ex.Model}, " "))
		if cam != "" {
			bits = append(bits, "相机 "+cam)
		}
		if ex.DateTime != "" {
			bits = append(bits, "拍摄时间 "+ex.DateTime)
		}
		if ex.HasGPS {
			// The coordinates are somebody's location: the card says the
			// tag is there, not where it points.
			bits = append(bits, "GPS 存在（坐标不显示）")
		}
		if len(bits) > 0 {
			exifPart = strings.Join(bits, "，")
		}
	}

	summary := fmt.Sprintf("分辨率 %dx%d（%s）；平均色 RGB(%d,%d,%d)，主色 %s 占 %.1f%%，非纯色比例 %.1f%%；EXIF：%s。",
		w, h, format, avgR, avgG, avgB, dominant, dominantPct, nonFlat, exifPart)
	tech := fmt.Sprintf("dHash=%s aHash=%s 分辨率=%dx%d format=%s bytes=%d",
		hashHex64(dh), hashHex64(ah), w, h, format, len(raw))

	if desc, verr := visionDescribe(context.Background(), raw, visionKeys()); verr == nil && desc != "" {
		summary += "视觉模型：" + desc
		tech += " vision=ok exif=stripped"
	}

	return MultimodalCard{
		Icon:       "🖼️",
		Title:      "图片指纹：" + base,
		Provider:   "本地解码",
		Status:     "✅",
		Badge:      fmt.Sprintf("%dx%d", w, h),
		Summary:    summary,
		TechDetail: tech,
	}, nil
}

// grayGrid resamples the image to a tw×th grid of 8-bit luminance by nearest
// neighbour. Hand-rolled so the package needs no image-scaling dependency.
func grayGrid(img image.Image, tw, th int) [][]uint8 {
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	grid := make([][]uint8, th)
	for ty := 0; ty < th; ty++ {
		row := make([]uint8, tw)
		sy := b.Min.Y + (2*ty+1)*sh/(2*th)
		if sy >= b.Max.Y {
			sy = b.Max.Y - 1
		}
		for tx := 0; tx < tw; tx++ {
			sx := b.Min.X + (2*tx+1)*sw/(2*tw)
			if sx >= b.Max.X {
				sx = b.Max.X - 1
			}
			r, g, bl, _ := img.At(sx, sy).RGBA()
			row[tx] = uint8(((299*r + 587*g + 114*bl) / 1000) >> 8)
		}
		grid[ty] = row
	}
	return grid
}

// dHash64 is the difference hash: 9×8 luminance, each pixel compared with
// its right neighbour, one bit each — 64 bits total.
func dHash64(img image.Image) uint64 {
	g := grayGrid(img, 9, 8)
	var v uint64
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			if g[y][x] < g[y][x+1] {
				v |= 1 << uint(y*8+x)
			}
		}
	}
	return v
}

// aHash64 is the average hash: 8×8 luminance, each pixel above the mean sets
// a bit.
func aHash64(img image.Image) uint64 {
	g := grayGrid(img, 8, 8)
	var sum uint32
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			sum += uint32(g[y][x])
		}
	}
	mean := sum / 64
	var v uint64
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			if uint32(g[y][x]) > mean {
				v |= 1 << uint(y*8+x)
			}
		}
	}
	return v
}

// hashHex64 renders a fingerprint as the 16 hex digits both sides compare.
func hashHex64(v uint64) string { return fmt.Sprintf("%016x", v) }

// imageStats samples the image (bounded to ~250k pixels) for the average
// colour, the dominant 4-bit-per-channel bucket, and how much of the image
// is not one flat colour. A fully flat image is a strong tell for a
// placeholder or a synthetic screenshot.
func imageStats(img image.Image) (avgR, avgG, avgB int, dominant string, dominantPct, nonFlat float64) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return 0, 0, 0, "#000000", 0, 0
	}
	step := 1
	if total := w * h; total > 250000 {
		step = int(math.Sqrt(float64(total) / 250000))
		if step < 1 {
			step = 1
		}
	}

	buckets := map[uint32]int{}
	exact := map[uint32]int{}
	var sumR, sumG, sumB, n uint64
	for y := 0; y < h; y += step {
		for x := 0; x < w; x += step {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			rr, gg, bb := uint8(r>>8), uint8(g>>8), uint8(bl>>8)
			sumR += uint64(rr)
			sumG += uint64(gg)
			sumB += uint64(bb)
			n++
			buckets[uint32(rr>>4)<<8|uint32(gg>>4)<<4|uint32(bb>>4)]++
			exact[uint32(rr)<<16|uint32(gg)<<8|uint32(bb)]++
		}
	}
	if n == 0 {
		return 0, 0, 0, "#000000", 0, 0
	}

	var topBucket uint32
	topBucketN := 0
	for k, v := range buckets {
		if v > topBucketN {
			topBucket, topBucketN = k, v
		}
	}
	topExactN := 0
	for _, v := range exact {
		if v > topExactN {
			topExactN = v
		}
	}

	avgR, avgG, avgB = int(sumR/n), int(sumG/n), int(sumB/n)
	dominant = fmt.Sprintf("#%02x%02x%02x",
		((topBucket>>8)&15)*17, ((topBucket>>4)&15)*17, (topBucket&15)*17)
	dominantPct = float64(topBucketN) / float64(n) * 100
	nonFlat = (1 - float64(topExactN)/float64(n)) * 100
	return
}

// exifInfo is what the JPEG APP1 parser recovered. A missing or malformed
// EXIF block is not an error: most images in the wild have none.
type exifInfo struct {
	Make, Model, DateTime string
	Lat, Lon              float64
	HasGPS                bool
}

// any says whether the block carried anything worth showing.
func (x exifInfo) any() bool {
	return x.Make != "" || x.Model != "" || x.DateTime != "" || x.HasGPS
}

// parseEXIF walks the JPEG segment chain to the APP1 block and parses the
// TIFF header inside it. Everything is bounds-checked: a truncated or
// hostile file yields an empty exifInfo rather than a panic.
func parseEXIF(raw []byte) exifInfo {
	if len(raw) < 4 || raw[0] != 0xFF || raw[1] != 0xD8 {
		return exifInfo{}
	}
	for i := 2; i+1 < len(raw); {
		if raw[i] != 0xFF {
			i++
			continue
		}
		marker := raw[i+1]
		if marker == 0xFF {
			i++
			continue
		}
		if marker == 0xD9 || marker == 0xDA {
			break
		}
		if marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) {
			i += 2
			continue
		}
		if i+4 > len(raw) {
			break
		}
		segLen := int(binary.BigEndian.Uint16(raw[i+2 : i+4]))
		if segLen < 2 {
			break
		}
		end := i + 2 + segLen
		if end > len(raw) {
			break
		}
		if marker == 0xE1 {
			seg := raw[i+4 : end]
			if len(seg) >= 6 && string(seg[:6]) == "Exif\x00\x00" {
				return parseTIFF(seg[6:])
			}
		}
		i = end
	}
	return exifInfo{}
}

// tiffEntry is one 12-byte IFD entry. raw is the offset of its 4-byte value
// field, which is where an inline value lives.
type tiffEntry struct {
	tag, typ uint16
	count    uint32
	raw      int
}

// tiffReader reads a TIFF block in its declared byte order.
type tiffReader struct {
	b     []byte
	order binary.ByteOrder
}

// u16 is a bounds-checked big/little-endian read; out of range reads 0.
func (r tiffReader) u16(off int) uint16 {
	if off < 0 || off+2 > len(r.b) {
		return 0
	}
	return r.order.Uint16(r.b[off:])
}

// u32 is the same guard for four-byte fields.
func (r tiffReader) u32(off int) uint32 {
	if off < 0 || off+4 > len(r.b) {
		return 0
	}
	return r.order.Uint32(r.b[off:])
}

// ifd returns the entries at off, silently stopping at the first one that
// would run past the block.
func (r tiffReader) ifd(off int) []tiffEntry {
	if off <= 0 || off+2 > len(r.b) {
		return nil
	}
	n := int(r.u16(off))
	if n <= 0 || n > 512 {
		return nil
	}
	base := off + 2
	if base+n*12 > len(r.b) {
		n = (len(r.b) - base) / 12
	}
	out := make([]tiffEntry, 0, n)
	for i := 0; i < n; i++ {
		p := base + i*12
		if p+12 > len(r.b) {
			break
		}
		out = append(out, tiffEntry{tag: r.u16(p), typ: r.u16(p + 2), count: r.u32(p + 4), raw: p + 8})
	}
	return out
}

// valueOffset is where an entry's data starts: inline in the entry when it
// fits in four bytes, else at the offset the entry holds.
func (r tiffReader) valueOffset(e tiffEntry, size int) int {
	if int(e.count)*size <= 4 {
		return e.raw
	}
	return int(r.u32(e.raw))
}

// ascii reads an ASCII entry, inline when it fits in four bytes.
func (r tiffReader) ascii(e tiffEntry) string {
	if e.typ != 2 || e.count == 0 {
		return ""
	}
	off := r.valueOffset(e, 1)
	if off <= 0 || off >= len(r.b) {
		return ""
	}
	n := int(e.count)
	if off+n > len(r.b) {
		n = len(r.b) - off
	}
	s := string(r.b[off : off+n])
	if i := strings.IndexByte(s, 0); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// rationalAt renders one TIFF rational, false when the denominator is zero.
func (r tiffReader) rationalAt(off int) (float64, bool) {
	if off < 0 || off+8 > len(r.b) {
		return 0, false
	}
	d := r.u32(off + 4)
	if d == 0 {
		return 0, false
	}
	return float64(r.u32(off)) / float64(d), true
}

// rationals reads a RATIONAL entry with up to eight values, as GPS uses.
func (r tiffReader) rationals(e tiffEntry) []float64 {
	if e.typ != 5 || e.count == 0 || e.count > 8 {
		return nil
	}
	off := r.valueOffset(e, 8)
	out := make([]float64, 0, e.count)
	for i := 0; i < int(e.count); i++ {
		v, ok := r.rationalAt(off + i*8)
		if !ok {
			return out
		}
		out = append(out, v)
	}
	return out
}

// parseTIFF walks IFD0, follows the Exif sub-IFD for DateTimeOriginal and the
// GPS IFD for coordinates.
func parseTIFF(t []byte) exifInfo {
	var info exifInfo
	if len(t) < 8 {
		return info
	}
	var order binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return info
	}
	r := tiffReader{b: t, order: order}
	if r.u16(2) != 42 {
		return info
	}
	entries := r.ifd(int(r.u32(4)))
	if entries == nil {
		return info
	}

	var exifIFD, gpsIFD uint32
	for _, e := range entries {
		switch e.tag {
		case 0x010F:
			info.Make = r.ascii(e)
		case 0x0110:
			info.Model = r.ascii(e)
		case 0x0132:
			info.DateTime = r.ascii(e)
		case 0x8769:
			exifIFD = r.u32(e.raw)
		case 0x8825:
			gpsIFD = r.u32(e.raw)
		}
	}

	if exifIFD != 0 {
		for _, e := range r.ifd(int(exifIFD)) {
			if e.tag == 0x9003 {
				if v := r.ascii(e); v != "" {
					info.DateTime = v
				}
			}
		}
	}
	if gpsIFD != 0 {
		parseGPS(r, r.ifd(int(gpsIFD)), &info)
	}
	return info
}

// parseGPS converts the three rationals of the GPS IFD into decimal degrees,
// negating by the S/W reference.
func parseGPS(r tiffReader, entries []tiffEntry, info *exifInfo) {
	var lat, lon []float64
	latRef, lonRef := "", ""
	for _, e := range entries {
		switch e.tag {
		case 1: // GPSLatitudeRef
			latRef = r.ascii(e)
		case 2: // GPSLatitude
			lat = r.rationals(e)
		case 3: // GPSLongitudeRef
			lonRef = r.ascii(e)
		case 4: // GPSLongitude
			lon = r.rationals(e)
		}
	}
	if len(lat) < 3 || len(lon) < 3 {
		return
	}
	info.Lat = lat[0] + lat[1]/60 + lat[2]/3600
	info.Lon = lon[0] + lon[1]/60 + lon[2]/3600
	if strings.HasPrefix(strings.ToUpper(latRef), "S") {
		info.Lat = -info.Lat
	}
	if strings.HasPrefix(strings.ToUpper(lonRef), "W") {
		info.Lon = -info.Lon
	}
	info.HasGPS = true
}

// visionKeys reads only the three vision settings. The image check has no
// Keys argument of its own, and resolving all thirteen vendor keys (each a
// providers.json scan) to describe one picture would be wasteful.
func visionKeys() Keys {
	return Keys{
		VisionAPIBase: lookup("MOLA_VISION_API_BASE", ""),
		VisionAPIKey:  lookup("MOLA_VISION_API_KEY", ""),
		VisionModel:   lookup("MOLA_VISION_MODEL", ""),
	}
}

// visionPost is the HTTP round trip, swappable in tests so the guarantee
// "no EXIF leaves the machine" is checked without a network.
var visionPost = postJSON

// visionReply is the OpenAI-compatible chat completion visionDescribe reads.
type visionReply struct {
	Choices []visionChoice `json:"choices"`
}

type visionChoice struct {
	Message visionMessage `json:"message"`
}

type visionMessage struct {
	Content string `json:"content"`
}

// stripImageMetadata re-encodes an image from decoded pixels, so EXIF
// (including GPS) and every other metadata block are gone before the bytes
// go to a remote vision model. It refuses a format it has no encoder for
// rather than forwarding the original bytes.
func stripImageMetadata(raw []byte) ([]byte, string, error) {
	img, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, "", fmt.Errorf("image: decode for EXIF strip: %w", err)
	}
	var buf bytes.Buffer
	switch format {
	case "jpeg":
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90})
	case "png":
		err = png.Encode(&buf, img)
	case "gif":
		err = gif.Encode(&buf, img, nil)
	default:
		return nil, "", fmt.Errorf("image: no metadata-stripping encoder for %q", format)
	}
	if err != nil {
		return nil, "", fmt.Errorf("image: re-encode for EXIF strip: %w", err)
	}
	return buf.Bytes(), format, nil
}

// visionDescribe asks an OpenAI-compatible vision model what is in the image.
// It is optional: no base/key configured means no call and no card noise.
// The bytes sent are always a re-encode of the decoded pixels, never the
// uploaded file: GPS and the camera's other EXIF fields stay local.
func visionDescribe(ctx context.Context, raw []byte, k Keys) (string, error) {
	if k.VisionAPIBase == "" || k.VisionAPIKey == "" {
		return "", nil
	}
	clean, cleanFormat, err := stripImageMetadata(raw)
	if err != nil {
		return "", err
	}
	mime := "image/jpeg"
	switch cleanFormat {
	case "png":
		mime = "image/png"
	case "gif":
		mime = "image/gif"
	}
	model := strings.TrimSpace(k.VisionModel)
	if model == "" {
		model = "gpt-4o-mini"
	}
	dataURI := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(clean)
	req := map[string]any{
		"model": model,
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "用中文一句话描述这张图片的主要内容、画面中可读的文字，以及任何异常或可疑之处。"},
					map[string]any{"type": "image_url", "image_url": map[string]string{"url": dataURI}},
				},
			},
		},
		"max_tokens": 200,
	}
	var out visionReply
	cctx, cancel := httpDeadline(ctx)
	defer cancel()
	if err := visionPost(cctx, chatCompletionsURL(k.VisionAPIBase), map[string]string{
		"Authorization": "Bearer " + k.VisionAPIKey,
	}, req, &out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", errors.New("vision: no choices")
	}
	desc := strings.TrimSpace(out.Choices[0].Message.Content)
	if desc == "" {
		return "", errors.New("vision: empty description")
	}
	return firstRunesOf(desc, 300), nil
}

// chatCompletionsURL accepts a base with or without the /v1 suffix, as both
// shapes are common in OpenAI-compatible gateways.
func chatCompletionsURL(base string) string {
	b := strings.TrimRight(strings.TrimSpace(base), "/")
	switch {
	case strings.HasSuffix(b, "/chat/completions"):
		return b
	case strings.HasSuffix(b, "/v1"):
		return b + "/chat/completions"
	default:
		return b + "/v1/chat/completions"
	}
}

// ---------------------------------------------------------------------------
// M27: video / Bilibili
// ---------------------------------------------------------------------------

// biliMeta is one video's first-party metadata.
type biliMeta struct {
	BVID, Title, Author   string
	Views, Danmaku, Likes int
	PubDate               int64
	Parts                 []biliPart
}

// biliPart is one page of a multi-part video.
type biliPart struct {
	Index    int
	Title    string
	Duration int
}

// danmakuItem is one bullet comment with its video-time offset.
type danmakuItem struct {
	TimeSec float64
	Text    string
}

var reBVID = regexp.MustCompile(`BV[0-9A-Za-z]{10}`)

// bvidIn finds a Bilibili BV id anywhere in a query.
func bvidIn(query string) string {
	return reBVID.FindString(query)
}

// videoCard is the M27 card. It prefers a BV id in the query, falls back to
// Bilibili's search when the query says it is about video, and attaches
// contact-sheet keyframes only when a local file is actually named.
func videoCard(ctx context.Context, query string, k Keys) (MultimodalCard, bool) {
	bvid := bvidIn(query)
	lower := strings.ToLower(query)
	intent := strings.Contains(query, "视频") || strings.Contains(query, "弹幕") ||
		strings.Contains(query, "B站") || strings.Contains(query, "b站") ||
		strings.Contains(lower, "bilibili")
	local := localVideoIn(query)
	var searchErr error
	if bvid == "" && intent {
		found, err := bilibiliSearchBVID(ctx, query)
		if err != nil {
			searchErr = err
		} else {
			bvid = found
		}
	}
	if bvid == "" && local == "" {
		if searchErr != nil {
			// The query asked about a video and Bilibili refused the search:
			// report that, rather than saying there is nothing to check.
			c := warnCard("🎬", "B站视频检索", "哔哩哔哩",
				"未带 BV 号，按关键词检索 B站失败："+searchErr.Error(), "https://search.bilibili.com/")
			c.Badge = "检索不可用"
			return c, true
		}
		return MultimodalCard{}, false
	}

	if bvid == "" {
		return localVideoCard(ctx, local), true
	}

	meta, cid, err := bilibiliView(ctx, bvid)
	if err != nil {
		c := warnCard("🎬", "B站视频核验", "哔哩哔哩",
			"已识别 "+bvid+"，但视频详情拉取失败："+err.Error(),
			"https://www.bilibili.com/video/"+bvid)
		c.Badge = "接口不可用"
		return c, true
	}

	var dms []danmakuItem
	if cid > 0 {
		if got, derr := bilibiliDanmaku(ctx, bvid, cid); derr == nil {
			dms = got
		}
	}
	kf, _ := ffmpegContactSheet(ctx, local)
	// Bilibili's view, search and danmaku endpoints are keyless; k is part of
	// the engine's uniform check signature, not something this check uses.
	_ = k
	return videoMetaCard(meta, dms, kf, local), true
}

// bilibiliVideoMeta is the public view call; bilibiliView also returns the
// first page's cid, which the danmaku endpoint needs and the meta struct
// deliberately does not carry.
func bilibiliVideoMeta(ctx context.Context, bvid string) (*biliMeta, error) {
	m, _, err := bilibiliView(ctx, bvid)
	return m, err
}

// bilibiliView calls the public view API for title, owner, counters, publish
// time and the page list.
func bilibiliView(ctx context.Context, bvid string) (*biliMeta, int64, error) {
	api := "https://api.bilibili.com/x/web-interface/view?bvid=" + url.QueryEscape(bvid)
	var out struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			BVID    string `json:"bvid"`
			Title   string `json:"title"`
			PubDate int64  `json:"pubdate"`
			Owner   struct {
				Name string `json:"name"`
			} `json:"owner"`
			Stat struct {
				View    int `json:"view"`
				Danmaku int `json:"danmaku"`
				Like    int `json:"like"`
			} `json:"stat"`
			Pages []struct {
				Page     int    `json:"page"`
				Part     string `json:"part"`
				Duration int    `json:"duration"`
				CID      int64  `json:"cid"`
			} `json:"pages"`
		} `json:"data"`
	}
	if err := fetchJSON(ctx, api, map[string]string{"Referer": "https://www.bilibili.com"}, &out); err != nil {
		return nil, 0, err
	}
	if out.Code != 0 {
		return nil, 0, fmt.Errorf("bilibili view code=%d message=%s", out.Code, out.Message)
	}
	m := &biliMeta{
		BVID:    out.Data.BVID,
		Title:   out.Data.Title,
		Author:  out.Data.Owner.Name,
		Views:   out.Data.Stat.View,
		Danmaku: out.Data.Stat.Danmaku,
		Likes:   out.Data.Stat.Like,
		PubDate: out.Data.PubDate,
	}
	var cid int64
	for _, p := range out.Data.Pages {
		m.Parts = append(m.Parts, biliPart{Index: p.Page, Title: p.Part, Duration: p.Duration})
		if cid == 0 {
			cid = p.CID
		}
	}
	if m.BVID == "" {
		m.BVID = bvid
	}
	if m.Title == "" {
		return nil, 0, errors.New("bilibili view: empty title")
	}
	return m, cid, nil
}

// bilibiliSearchBVID resolves a natural-language video query to its first
// hit. Bilibili requires the Referer for this endpoint from a non-browser.
func bilibiliSearchBVID(ctx context.Context, query string) (string, error) {
	api := "https://api.bilibili.com/x/web-interface/search/type?search_type=video&page=1&page_size=5&keyword=" +
		url.QueryEscape(query)
	var out struct {
		Code int `json:"code"`
		Data struct {
			Result []struct {
				BVID  string `json:"bvid"`
				Title string `json:"title"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := fetchJSON(ctx, api, map[string]string{"Referer": "https://www.bilibili.com"}, &out); err != nil {
		return "", err
	}
	if out.Code != 0 {
		return "", fmt.Errorf("bilibili search code=%d", out.Code)
	}
	for _, r := range out.Data.Result {
		if r.BVID != "" {
			return r.BVID, nil
		}
	}
	return "", errors.New("bilibili search: no result")
}

// bilibiliDanmaku reads the XML danmaku list for one page. The endpoint has
// historically answered both plain XML and a deflate stream, so a parse
// failure retries after inflating the body.
func bilibiliDanmaku(ctx context.Context, bvid string, cid int64) ([]danmakuItem, error) {
	if cid <= 0 {
		return nil, errors.New("danmaku: no cid")
	}
	u := "https://api.bilibili.com/x/v1/dm/list.so?oid=" + strconv.FormatInt(cid, 10)
	body, err := fetchTextWithHeaders(ctx, u, map[string]string{
		"Referer": "https://www.bilibili.com/video/" + bvid,
		"Accept":  "application/xml,text/xml,*/*",
	})
	if err != nil {
		return nil, err
	}
	items, perr := parseDanmakuXML(body)
	if perr == nil && len(items) > 0 {
		return items, nil
	}
	if inflated, derr := inflateBytes([]byte(body)); derr == nil {
		if got, e2 := parseDanmakuXML(string(inflated)); e2 == nil && len(got) > 0 {
			return got, nil
		}
	}
	return items, perr
}

// fetchTextWithHeaders is fetchGet plus the vendor headers some Bilibili
// endpoints insist on.
func fetchTextWithHeaders(ctx context.Context, u string, hdr map[string]string) (string, error) {
	req, err := httpNewRequest(ctx, http.MethodGet, u, "")
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", ua)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	b, err := doBytes(req)
	return string(b), err
}

// parseDanmakuXML reads <d p="time,mode,size,color,...">text</d> entries.
func parseDanmakuXML(s string) ([]danmakuItem, error) {
	var doc struct {
		D []struct {
			P    string `xml:"p,attr"`
			Text string `xml:",chardata"`
		} `xml:"d"`
	}
	if err := xml.Unmarshal([]byte(s), &doc); err != nil {
		return nil, err
	}
	out := make([]danmakuItem, 0, len(doc.D))
	for _, d := range doc.D {
		first, _, _ := strings.Cut(d.P, ",")
		text := strings.TrimSpace(d.Text)
		if text == "" {
			continue
		}
		out = append(out, danmakuItem{TimeSec: atof(first), Text: text})
		if len(out) >= 20000 {
			break
		}
	}
	return out, nil
}

// inflateBytes tries the two streams Bilibili has used for this endpoint.
func inflateBytes(b []byte) ([]byte, error) {
	r := flate.NewReader(bytes.NewReader(b))
	if out, err := io.ReadAll(io.LimitReader(r, maxBody)); err == nil && len(out) > 0 {
		r.Close()
		return out, nil
	}
	r.Close()
	if r, err := zlib.NewReader(bytes.NewReader(b)); err == nil {
		defer r.Close()
		if out, err := io.ReadAll(io.LimitReader(r, maxBody)); err == nil && len(out) > 0 {
			return out, nil
		}
	}
	return nil, errors.New("inflate: not deflate or zlib")
}

// reDanmakuLatin is the only shape worth counting in Latin script; the
// tokens it yields are alphanumeric, so no punctuation filter is needed
// beside it.
var reDanmakuLatin = regexp.MustCompile(`[A-Za-z0-9]{2,12}`)

// danmakuTopWords counts what the crowd repeats: Latin runs and the two-rune
// grams of each Han run, which is how Chinese danmaku memes surface.
func danmakuTopWords(items []danmakuItem, n int) []string {
	freq := map[string]int{}
	for _, it := range items {
		for _, w := range reDanmakuLatin.FindAllString(it.Text, -1) {
			freq[strings.ToLower(w)]++
		}
		runes := []rune(it.Text)
		for i := 0; i+1 < len(runes); i++ {
			if !unicode.Is(unicode.Han, runes[i]) || !unicode.Is(unicode.Han, runes[i+1]) {
				continue
			}
			freq[string(runes[i:i+2])]++
		}
	}
	type kv struct {
		k string
		v int
	}
	var kvs []kv
	for k, v := range freq {
		if v >= 2 {
			kvs = append(kvs, kv{k, v})
		}
	}
	sort.Slice(kvs, func(i, j int) bool {
		if kvs[i].v != kvs[j].v {
			return kvs[i].v > kvs[j].v
		}
		return kvs[i].k < kvs[j].k
	})
	out := make([]string, 0, n)
	for _, e := range kvs {
		out = append(out, fmt.Sprintf("%s×%d", e.k, e.v))
		if len(out) >= n {
			break
		}
	}
	return out
}

// danmakuPeaks finds the three busiest five-second windows, which mark the
// moments the crowd reacted to.
func danmakuPeaks(items []danmakuItem) []string {
	if len(items) == 0 {
		return nil
	}
	buckets := map[int]int{}
	for _, it := range items {
		if it.TimeSec < 0 {
			continue
		}
		buckets[int(it.TimeSec)/5*5]++
	}
	type kv struct {
		t, n int
	}
	kvs := make([]kv, 0, len(buckets))
	for t, n := range buckets {
		kvs = append(kvs, kv{t, n})
	}
	sort.Slice(kvs, func(i, j int) bool {
		if kvs[i].n != kvs[j].n {
			return kvs[i].n > kvs[j].n
		}
		return kvs[i].t < kvs[j].t
	})
	if len(kvs) > 3 {
		kvs = kvs[:3]
	}
	out := make([]string, 0, len(kvs))
	for _, e := range kvs {
		out = append(out, fmt.Sprintf("%02d:%02d（%d条）", e.t/60, e.t%60, e.n))
	}
	return out
}

// videoMetaCard renders what the Bilibili calls and the local contact sheet
// actually returned.
func videoMetaCard(m *biliMeta, dms []danmakuItem, kf []KeyframeItem, local string) MultimodalCard {
	parts := make([]string, 0, len(m.Parts))
	for i, p := range m.Parts {
		if i >= 5 {
			parts = append(parts, "…")
			break
		}
		parts = append(parts, fmt.Sprintf("P%d %s（%ds）", p.Index, p.Title, p.Duration))
	}
	summary := fmt.Sprintf("UP主：%s；播放 %s；弹幕 %s；点赞 %s；发布 %s；分P %d 个：%s。",
		m.Author, fmtCount(m.Views), fmtCount(m.Danmaku), fmtCount(m.Likes),
		unixDate(m.PubDate), len(m.Parts), strings.Join(parts, "、"))
	if len(dms) > 0 {
		summary += fmt.Sprintf("弹幕样本 %d 条；高频词：%s；峰值：%s。",
			len(dms), strings.Join(danmakuTopWords(dms, 10), "、"), strings.Join(danmakuPeaks(dms), "、"))
	} else {
		summary += "弹幕接口未返回数据。"
	}
	switch {
	case local == "":
		if _, err := exec.LookPath("ffmpeg"); err != nil {
			summary += "未检测到 ffmpeg，且未提供本地视频文件，未生成关键帧。"
		} else {
			summary += "未提供本地视频文件，未生成关键帧。"
		}
	case len(kf) == 0:
		summary += "本地视频关键帧生成失败。"
	default:
		summary += fmt.Sprintf("已从本地视频生成 %d 帧关键帧。", len(kf))
	}

	return MultimodalCard{
		Icon:     "🎬",
		Title:    m.Title,
		Provider: "哔哩哔哩",
		Status:   "✅",
		Badge:    "BV " + m.BVID,
		Summary:  summary,
		TechDetail: fmt.Sprintf("bvid=%s views=%d danmaku=%d like=%d parts=%d danmakuSample=%d keyframes=%d",
			m.BVID, m.Views, m.Danmaku, m.Likes, len(m.Parts), len(dms), len(kf)),
		SourceURL: "https://www.bilibili.com/video/" + m.BVID,
		Keyframes: kf,
	}
}

// localVideoCard is the card when only a local file was named: no metadata
// exists to fetch, but the frames are real.
func localVideoCard(ctx context.Context, path string) MultimodalCard {
	kf, _ := ffmpegContactSheet(ctx, path)
	status, badge := "✅", "本地视频"
	summary := fmt.Sprintf("本地视频 %s；已生成 %d 帧关键帧（查询未带 BV 号，无弹幕可分析）。", filepath.Base(path), len(kf))
	if len(kf) == 0 {
		status, badge = "⚠️", "关键帧不可用"
		if _, err := exec.LookPath("ffmpeg"); err != nil {
			summary = fmt.Sprintf("本地视频 %s；未检测到 ffmpeg，未生成关键帧。", filepath.Base(path))
		} else {
			summary = fmt.Sprintf("本地视频 %s；ffmpeg 已找到，但关键帧生成失败。", filepath.Base(path))
		}
	}
	return MultimodalCard{
		Icon:       "🎬",
		Title:      "本地视频关键帧：" + filepath.Base(path),
		Provider:   "ffmpeg",
		Status:     status,
		Badge:      badge,
		Summary:    summary,
		TechDetail: fmt.Sprintf("ffmpeg=local frames=%d path=%s", len(kf), path),
		Keyframes:  kf,
	}
}

var videoExts = map[string]bool{
	".mp4": true, ".mkv": true, ".mov": true, ".avi": true,
	".flv": true, ".webm": true, ".m4v": true, ".ts": true, ".wmv": true,
}

// localVideoIn finds a quoted or whitespace-separated token in the query
// that names an existing video file. Without this, ffmpegContactSheet would
// never be reachable from the engine, which has no file-path option.
func localVideoIn(query string) string {
	var candidates []string
	for i := 0; i < len(query); i++ {
		if query[i] != '"' && query[i] != '\'' {
			continue
		}
		q := query[i]
		j := strings.IndexByte(query[i+1:], q)
		if j < 0 {
			break
		}
		candidates = append(candidates, query[i+1:i+1+j])
		i += j + 1
	}
	candidates = append(candidates, strings.Fields(query)...)
	for _, c := range candidates {
		c = strings.Trim(c, `"'（）()，,。；;`)
		if c == "" || !videoExts[strings.ToLower(filepath.Ext(c))] {
			continue
		}
		if st, err := os.Stat(c); err == nil && !st.IsDir() && st.Size() > 0 {
			return c
		}
	}
	return ""
}

var reFFDuration = regexp.MustCompile(`Duration:\s*(\d+):(\d+):(\d+(?:\.\d+)?)`)

// ffmpegContactSheet renders a 3×3 sheet sampled evenly across the file and
// turns it into nine keyframe items. No ffmpeg, no file, or a failed render
// all return an empty slice and a nil error: the card states which.
func ffmpegContactSheet(ctx context.Context, videoPath string) ([]KeyframeItem, error) {
	if videoPath == "" {
		return []KeyframeItem{}, nil
	}
	if st, err := os.Stat(videoPath); err != nil || st.IsDir() || st.Size() == 0 {
		return []KeyframeItem{}, nil
	}
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		return []KeyframeItem{}, nil
	}

	dur := ffmpegDuration(ctx, ff, videoPath)
	// Nine frames spread across the file. A very short clip would need a
	// frame rate above 30, which ffmpeg refuses to tile cleanly, so the rate
	// is capped and the timestamps below fall back to the estimate.
	fps := 1.0 / 2.0
	if dur > 0 {
		fps = 9 / dur
		if fps > 30 {
			fps = 30
		}
	}
	vf := fmt.Sprintf("fps=%s,scale=320:-1,tile=3x3", strconv.FormatFloat(fps, 'f', 6, 64))
	out := filepath.Join(os.TempDir(),
		"molasearch-keysheet-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".jpg")
	defer os.Remove(out)

	cmd := proc.CommandContext(ctx, ff,
		"-hide_banner", "-loglevel", "error",
		"-i", videoPath,
		"-vf", vf,
		"-frames:v", "1", "-y", out)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return []KeyframeItem{}, nil
	}
	if st, err := os.Stat(out); err != nil || st.Size() < 1024 {
		return []KeyframeItem{}, nil
	}

	items := make([]KeyframeItem, 0, 9)
	for i := 0; i < 9; i++ {
		ts := "~" + strconv.Itoa((i+1)*2) + "s"
		if dur > 0 {
			sec := dur * (float64(i) + 0.5) / 9
			ts = fmt.Sprintf("%02d:%02d", int(sec)/60, int(sec)%60)
		}
		items = append(items, KeyframeItem{TS: ts, Desc: "关键帧 #" + strconv.Itoa(i+1)})
	}
	return items, nil
}

// ffmpegDuration reads the container's duration from ffmpeg's own banner;
// no ffprobe binary is assumed.
func ffmpegDuration(ctx context.Context, ffmpeg, path string) float64 {
	cmd := proc.CommandContext(ctx, ffmpeg, "-hide_banner", "-i", path)
	var errBuf bytes.Buffer
	cmd.Stdout = io.Discard
	cmd.Stderr = &errBuf
	_ = cmd.Run() // ffmpeg exits non-zero for a probe, which is expected
	m := reFFDuration.FindStringSubmatch(errBuf.String())
	if len(m) != 4 {
		return 0
	}
	h := atoiOr(m[1], 0)
	mi := atoiOr(m[2], 0)
	sec := atof(m[3])
	return float64(h*3600+mi*60) + sec
}

// fmtCount groups a counter with thousands separators for readability.
func fmtCount(n int) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.Itoa(n)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// M28/M29: DOM accessibility tree and Android UI dump
// ---------------------------------------------------------------------------

// domSnapshot fetches a page and renders a compact Set-of-Mark tree of its
// landmarks, headings and interactive elements, each numbered for an agent
// to reference.
func domSnapshot(ctx context.Context, pageURL string) (MultimodalCard, error) {
	body, err := fetchGet(ctx, pageURL, "text/html,application/xhtml+xml")
	if err != nil {
		return MultimodalCard{}, err
	}
	doc, err := parseHTML(body)
	if err != nil {
		return MultimodalCard{}, err
	}
	tree, nodes, title := a11yTree(doc)
	if title == "" {
		title = "(无标题)"
	}
	return MultimodalCard{
		Icon:       "🧭",
		Title:      "DOM 无障碍树快照",
		Provider:   domainOf(pageURL),
		Status:     "✅",
		Badge:      fmt.Sprintf("%d 节点", nodes),
		Summary:    fmt.Sprintf("页面标题：%s；可访问性树 %d 个节点、%d 字节。", title, nodes, len(tree)),
		TechDetail: fmt.Sprintf("nodes=%d bytes=%d url=%s", nodes, len(tree), pageURL),
		SourceURL:  pageURL,
	}, nil
}

// a11yWalker accumulates the semantic tree under a byte budget, so a huge
// page cannot turn the card into a page dump.
type a11yWalker struct {
	b      strings.Builder
	nodes  int
	marks  int
	budget int
}

// emit appends one indented line if it fits the remaining budget.
func (w *a11yWalker) emit(depth int, line string) {
	if w.budget <= 0 {
		return
	}
	indent := strings.Repeat("  ", depth)
	if len(indent)+len(line)+1 > w.budget {
		w.budget = 0
		return
	}
	w.b.WriteString(indent)
	w.b.WriteString(line)
	w.b.WriteByte('\n')
	w.budget -= len(indent) + len(line) + 1
	w.nodes++
}

// a11yTree walks the document once. It returns the rendered tree, the number
// of emitted nodes and the page title.
func a11yTree(root *htmlNode) (string, int, string) {
	w := &a11yWalker{budget: 1500}
	title := ""
	var walk func(n *htmlNode, depth int)
	walk = func(n *htmlNode, depth int) {
		if n.Type != elementNode {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c, depth)
			}
			return
		}
		tag := strings.ToLower(n.Data)
		switch tag {
		case "script", "style", "noscript", "svg", "template", "iframe":
			return
		case "head":
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == elementNode && strings.EqualFold(c.Data, "title") && title == "" {
					title = strings.TrimSpace(nodeText(c))
				}
			}
			return
		case "title":
			if title == "" {
				title = strings.TrimSpace(nodeText(n))
			}
			return
		}
		if depth > 6 {
			depth = 6
		}
		if line, landmark, ok := a11yLine(w, n, tag); ok {
			w.emit(depth, line)
			if landmark {
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c, depth+1)
				}
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, depth)
		}
	}
	walk(root, 0)
	return w.b.String(), w.nodes, title
}

// a11yLine renders one element, or says it is not one the tree carries.
// Landmarks are descended into; interactive and heading elements are leaves.
func a11yLine(w *a11yWalker, n *htmlNode, tag string) (line string, landmark, ok bool) {
	switch tag {
	case "header", "nav", "main", "footer", "article", "section", "form", "aside":
		if name := accessibleName(n); name != "" {
			return fmt.Sprintf("[%s] %q", tag, name), true, true
		}
		return "[" + tag + "]", true, true
	case "h1", "h2", "h3":
		name := accessibleName(n)
		if name == "" {
			return "", false, false
		}
		return fmt.Sprintf("[%s] %q", tag, name), false, true
	case "a":
		name := accessibleName(n)
		if name == "" {
			return "", false, false
		}
		w.marks++
		line = fmt.Sprintf("#%d [a] %q", w.marks, name)
		if href := strings.TrimSpace(attrOf(n, "href")); href != "" {
			line += " -> " + firstRunesOf(href, 80)
		}
		return line, false, true
	case "button":
		name := accessibleName(n)
		if name == "" {
			return "", false, false
		}
		w.marks++
		return fmt.Sprintf("#%d [button] %q", w.marks, name), false, true
	case "input":
		typ := strings.ToLower(strings.TrimSpace(attrOf(n, "type")))
		if typ == "" {
			typ = "text"
		}
		if typ == "hidden" {
			return "", false, false
		}
		w.marks++
		line = fmt.Sprintf("#%d [input type=%s]", w.marks, typ)
		if name := accessibleName(n); name != "" {
			line += fmt.Sprintf(" %q", name)
		}
		return line, false, true
	case "select", "textarea":
		w.marks++
		line = fmt.Sprintf("#%d [%s]", w.marks, tag)
		if name := accessibleName(n); name != "" {
			line += fmt.Sprintf(" %q", name)
		}
		return line, false, true
	}
	return "", false, false
}

// accessibleName is the accessible name an agent would use: explicit ARIA
// first, then title/alt/placeholder, then the element's text.
func accessibleName(n *htmlNode) string {
	for _, key := range []string{"aria-label", "title", "alt", "placeholder"} {
		if v := strings.TrimSpace(attrOf(n, key)); v != "" {
			return firstRunesOf(v, 40)
		}
	}
	return firstRunesOf(nodeText(n), 40)
}

// adbSnapshot dumps the connected Android device's UI hierarchy. ok is false
// when adb is absent or no device is attached — there is nothing to report.
func adbSnapshot(ctx context.Context) (MultimodalCard, bool) {
	adb, err := exec.LookPath("adb")
	if err != nil {
		return MultimodalCard{}, false
	}
	dctx, dcancel := context.WithTimeout(ctx, 2*time.Second)
	devOut, derr := runCmdOut(dctx, adb, "devices")
	dcancel()
	if derr != nil {
		return MultimodalCard{}, false
	}
	devices := adbDevices(devOut)
	if len(devices) == 0 {
		return MultimodalCard{}, false
	}
	serial := devices[0]

	tctx, tcancel := context.WithTimeout(ctx, 8*time.Second)
	_, dumpErr := runCmdOut(tctx, adb, "-s", serial, "shell", "uiautomator", "dump", "/sdcard/w.xml")
	xmlOut, catErr := runCmdOut(tctx, adb, "-s", serial, "shell", "cat", "/sdcard/w.xml")
	tcancel()

	if dumpErr != nil || catErr != nil || strings.TrimSpace(xmlOut) == "" {
		reason := "组件树导出失败"
		if dumpErr != nil {
			reason += "：" + dumpErr.Error()
		} else if catErr != nil {
			reason += "：" + catErr.Error()
		}
		card := warnCard("📱", "Android 无障碍组件树", "adb / uiautomator",
			fmt.Sprintf("设备 %s 已连接（共 %d 台），但%s。", serial, len(devices), reason), "")
		card.TechDetail = "devices=" + itoa(len(devices))
		return card, true
	}

	st, serr := adbTreeStats(xmlOut)
	if serr != nil {
		card := warnCard("📱", "Android 无障碍组件树", "adb / uiautomator",
			fmt.Sprintf("设备 %s 的组件树 XML 解析失败：%v", serial, serr), "")
		card.TechDetail = "devices=" + itoa(len(devices))
		return card, true
	}

	classes := make([]string, 0, len(st.classes))
	for c := range st.classes {
		classes = append(classes, c)
	}
	sort.Slice(classes, func(i, j int) bool {
		if st.classes[classes[i]] != st.classes[classes[j]] {
			return st.classes[classes[i]] > st.classes[classes[j]]
		}
		return classes[i] < classes[j]
	})
	if len(classes) > 5 {
		classes = classes[:5]
	}
	classBits := make([]string, 0, len(classes))
	for _, c := range classes {
		classBits = append(classBits, fmt.Sprintf("%s×%d", strings.TrimPrefix(c, "android.widget."), st.classes[c]))
	}

	return MultimodalCard{
		Icon:     "📱",
		Title:    "Android 无障碍组件树",
		Provider: "adb / uiautomator",
		Status:   "✅",
		Badge:    fmt.Sprintf("%d 台设备", len(devices)),
		Summary: fmt.Sprintf("设备 %s（共 %d 台）：组件 %d 个，可点击 %d 个，有文本 %d 个，有 resource-id %d 个；类名 Top：%s。",
			serial, len(devices), st.nodes, st.clickable, st.withText, st.withID, strings.Join(classBits, "、")),
		TechDetail: fmt.Sprintf("devices=%d nodes=%d clickable=%d text=%d resourceId=%d classes=%d xmlBytes=%d",
			len(devices), st.nodes, st.clickable, st.withText, st.withID, len(st.classes), len(xmlOut)),
	}, true
}

// adbDevices parses `adb devices` into the attached serials, skipping the
// header and any device that is not in the "device" state.
func adbDevices(out string) []string {
	var devices []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) >= 2 && f[1] == "device" {
			devices = append(devices, f[0])
		}
	}
	return devices
}

// adbStats counts what the uiautomator XML actually contains.
type adbStats struct {
	nodes, clickable, withText, withID int
	classes                            map[string]int
}

// adbTreeStats walks the uiautomator XML and counts what it contains.
func adbTreeStats(xmlOut string) (adbStats, error) {
	st := adbStats{classes: map[string]int{}}
	dec := xml.NewDecoder(strings.NewReader(xmlOut))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return st, err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		st.nodes++
		for _, a := range se.Attr {
			switch a.Name.Local {
			case "class":
				if a.Value != "" {
					st.classes[a.Value]++
				}
			case "text":
				if strings.TrimSpace(a.Value) != "" {
					st.withText++
				}
			case "resource-id":
				if strings.TrimSpace(a.Value) != "" {
					st.withID++
				}
			case "clickable":
				if a.Value == "true" {
					st.clickable++
				}
			}
		}
	}
	return st, nil
}

// runCmdOut runs a command and returns only stdout: adb's progress chatter on
// stderr must not contaminate the XML.
func runCmdOut(ctx context.Context, bin string, args ...string) (string, error) {
	cmd := proc.CommandContext(ctx, bin, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	err := cmd.Run()
	return out.String(), err
}
