package driver

import (
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"

	"modernc.org/sqlite"
)

// GeoPackage spatial index triggers (rtree_<table>_<column>_*) call
// ST_IsEmpty and ST_MinX/MaxX/MinY/MaxY. Upstream geodiff registers them
// through libgpkg; these follow libgpkg's behaviour for GeoPackage blobs:
//
//   - NULL or a zero-length value gives NULL.
//   - ST_IsEmpty reads the empty flag of the blob header.
//   - ST_Min/Max read the header envelope when there is one, otherwise the
//     envelope of the ISO WKB body; an empty geometry gives NULL.
//   - A value that isn't a GeoPackage geometry blob is an error, which fails
//     the statement (and so the apply).
//
// modernc.org/sqlite registers functions for the whole process. They are
// registered when the first SQLite driver opens, so a caller that registered
// a function of the same name before that keeps its own.
var registerGpkgFunctionsOnce sync.Once

func registerGpkgFunctions() {
	registerGpkgFunctionsOnce.Do(func() {
		register := func(name string, fn func([]byte) (driver.Value, error)) {
			err := sqlite.RegisterDeterministicScalarFunction(name, 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
				blob := gpkgFunctionArg(args[0])
				if len(blob) == 0 {
					return nil, nil
				}
				return fn(blob)
			})
			if err != nil && !strings.Contains(err.Error(), "already registered") {
				panic(err)
			}
		}
		register("ST_IsEmpty", func(blob []byte) (driver.Value, error) {
			h, err := readGpkgHeader(blob)
			if err != nil {
				return nil, err
			}
			if h.empty {
				return int64(1), nil
			}
			return int64(0), nil
		})
		envelopeFunc := func(pick func(gpkgEnvelope) float64) func([]byte) (driver.Value, error) {
			return func(blob []byte) (driver.Value, error) {
				env, err := gpkgBlobEnvelope(blob)
				if err != nil {
					return nil, err
				}
				if !env.ok {
					return nil, nil
				}
				return pick(env), nil
			}
		}
		register("ST_MinX", envelopeFunc(func(e gpkgEnvelope) float64 { return e.minX }))
		register("ST_MaxX", envelopeFunc(func(e gpkgEnvelope) float64 { return e.maxX }))
		register("ST_MinY", envelopeFunc(func(e gpkgEnvelope) float64 { return e.minY }))
		register("ST_MaxY", envelopeFunc(func(e gpkgEnvelope) float64 { return e.maxY }))
	})
}

// gpkgFunctionArg returns the bytes SQLite's sqlite3_value_blob would give.
func gpkgFunctionArg(v driver.Value) []byte {
	switch v := v.(type) {
	case nil:
		return nil
	case []byte:
		return v
	case string:
		return []byte(v)
	default:
		return []byte(fmt.Sprint(v))
	}
}

type gpkgHeader struct {
	empty bool
	env   gpkgEnvelope // ok when the header carries an envelope
	size  int          // header length; the WKB body follows
}

type gpkgEnvelope struct {
	ok                     bool
	minX, maxX, minY, maxY float64
}

func newGpkgEnvelope() gpkgEnvelope {
	return gpkgEnvelope{minX: math.MaxFloat64, maxX: -math.MaxFloat64, minY: math.MaxFloat64, maxY: -math.MaxFloat64}
}

// add widens the envelope like libgpkg: a NaN coordinate compares false and
// leaves its bound alone.
func (e *gpkgEnvelope) add(x, y float64) {
	e.ok = true
	if x < e.minX {
		e.minX = x
	}
	if x > e.maxX {
		e.maxX = x
	}
	if y < e.minY {
		e.minY = y
	}
	if y > e.maxY {
		e.maxY = y
	}
}

var errGpkgBlob = errors.New("Invalid geometry blob header")

func readGpkgHeader(blob []byte) (gpkgHeader, error) {
	var h gpkgHeader
	if len(blob) < 8 {
		return h, errGpkgBlob
	}
	if blob[0] != 'G' || blob[1] != 'P' {
		return h, fmt.Errorf("Incorrect GPB magic number [expected: GP, actual:%q]", blob[:2])
	}
	if blob[2] != 0 {
		return h, fmt.Errorf("Incorrect GPB version [expected: 0, actual:%d]", blob[2])
	}
	flags := blob[3]
	h.empty = (flags>>4)&1 == 1
	envelope := (flags >> 1) & 7
	var order binary.ByteOrder = binary.BigEndian
	if flags&1 == 1 {
		order = binary.LittleEndian
	}
	// Envelope doubles: none, XY, XYZ, XYZM, XYM.
	doubles := map[byte]int{0: 0, 1: 4, 2: 6, 3: 8, 4: 6}
	n, ok := doubles[envelope]
	if !ok {
		return h, fmt.Errorf("Incorrect GPB envelope value: [expected: [0-4], actual:%d]", envelope)
	}
	h.size = 8 + 8*n
	if len(blob) < h.size {
		return h, errGpkgBlob
	}
	if n > 0 {
		d := func(i int) float64 { return math.Float64frombits(order.Uint64(blob[8+8*i:])) }
		h.env = gpkgEnvelope{ok: true, minX: d(0), maxX: d(1), minY: d(2), maxY: d(3)}
	}
	return h, nil
}

// gpkgBlobEnvelope is the XY envelope ST_MinX and friends report.
func gpkgBlobEnvelope(blob []byte) (gpkgEnvelope, error) {
	h, err := readGpkgHeader(blob)
	if err != nil {
		return gpkgEnvelope{}, err
	}
	if h.env.ok {
		return h.env, nil
	}
	r := &wkbEnvelopeReader{buf: blob[h.size:]}
	env := newGpkgEnvelope()
	if err := r.geometry(&env, -1, nil); err != nil {
		return gpkgEnvelope{}, err
	}
	return env, nil
}

// ISO WKB geometry types libgpkg reads.
const (
	wkbPoint              = 1
	wkbLineString         = 2
	wkbPolygon            = 3
	wkbMultiPoint         = 4
	wkbMultiLineString    = 5
	wkbMultiPolygon       = 6
	wkbGeometryCollection = 7
	wkbCircularString     = 8
	wkbCompoundCurve      = 9
	wkbCurvePolygon       = 10
)

type wkbEnvelopeReader struct {
	buf   []byte
	order binary.ByteOrder
}

var errWKBShort = errors.New("Error reading geometry: WKB too short")

func (r *wkbEnvelopeReader) u32() (uint32, error) {
	if len(r.buf) < 4 {
		return 0, errWKBShort
	}
	v := r.order.Uint32(r.buf)
	r.buf = r.buf[4:]
	return v, nil
}

func (r *wkbEnvelopeReader) f64() (float64, error) {
	if len(r.buf) < 8 {
		return 0, errWKBShort
	}
	v := math.Float64frombits(r.order.Uint64(r.buf))
	r.buf = r.buf[8:]
	return v, nil
}

// geometry reads one WKB geometry with its header. dims is the parent's
// coordinate dimension (-1 at the top) and allowed the member types a parent
// accepts (nil for any); libgpkg rejects members that differ from either.
func (r *wkbEnvelopeReader) geometry(env *gpkgEnvelope, dims int, allowed []uint32) error {
	if len(r.buf) < 1 {
		return errWKBShort
	}
	if r.buf[0] == 0 {
		r.order = binary.BigEndian
	} else {
		r.order = binary.LittleEndian
	}
	r.buf = r.buf[1:]
	code, err := r.u32()
	if err != nil {
		return fmt.Errorf("Error reading geometry type")
	}
	modifier, typ := code/1000, code%1000
	if modifier > 3 {
		return fmt.Errorf("Unsupported geometry modifier: %d", modifier*1000)
	}
	if typ < wkbPoint || typ > wkbCurvePolygon {
		return fmt.Errorf("Unsupported WKB geometry type: %d", typ)
	}
	if dims >= 0 && int(modifier) != dims {
		return errGpkgBlob
	}
	if allowed != nil && !containsType(allowed, typ) {
		return errGpkgBlob
	}
	coords := 2
	if modifier == 1 || modifier == 2 {
		coords = 3
	} else if modifier == 3 {
		coords = 4
	}
	member := func(allowed ...uint32) error {
		n, err := r.u32()
		if err != nil {
			return err
		}
		for i := uint32(0); i < n; i++ {
			if err := r.geometry(env, int(modifier), allowed); err != nil {
				return err
			}
		}
		return nil
	}
	switch typ {
	case wkbPoint:
		p, err := r.points(1, coords)
		if err != nil {
			return err
		}
		// A point whose every ordinate is NaN is empty.
		for _, v := range p[0] {
			if !math.IsNaN(v) {
				env.add(p[0][0], p[0][1])
				break
			}
		}
	case wkbLineString:
		return r.pointList(env, coords, false)
	case wkbCircularString:
		return r.pointList(env, coords, true)
	case wkbPolygon:
		rings, err := r.u32()
		if err != nil {
			return err
		}
		for i := uint32(0); i < rings; i++ {
			if err := r.pointList(env, coords, false); err != nil {
				return err
			}
		}
	case wkbMultiPoint:
		return member(wkbPoint)
	case wkbMultiLineString:
		return member(wkbLineString)
	case wkbMultiPolygon:
		return member(wkbPolygon)
	case wkbGeometryCollection:
		return member()
	case wkbCompoundCurve:
		return member(wkbLineString, wkbCircularString)
	case wkbCurvePolygon:
		return member(wkbLineString, wkbCircularString, wkbCompoundCurve)
	}
	return nil
}

func containsType(types []uint32, t uint32) bool {
	for _, v := range types {
		if v == t {
			return true
		}
	}
	return false
}

func (r *wkbEnvelopeReader) points(n uint32, coords int) ([][]float64, error) {
	if uint64(len(r.buf)) < uint64(n)*uint64(coords)*8 {
		return nil, errWKBShort
	}
	out := make([][]float64, n)
	for i := range out {
		p := make([]float64, coords)
		for j := range p {
			p[j], _ = r.f64()
		}
		out[i] = p
	}
	return out, nil
}

// pointList reads a counted point sequence: a line string, a ring, or a
// circular string, whose bounds include each arc's extent.
func (r *wkbEnvelopeReader) pointList(env *gpkgEnvelope, coords int, arcs bool) error {
	n, err := r.u32()
	if err != nil {
		return err
	}
	if arcs && n != 0 && (n < 3 || (n-3)%2 != 0) {
		return errors.New("Error CircularString requires 3+2n points or has to be EMPTY")
	}
	pts, err := r.points(n, coords)
	if err != nil {
		return err
	}
	if !arcs {
		for _, p := range pts {
			env.add(p[0], p[1])
		}
		return nil
	}
	for i := 0; i+2 < len(pts); i += 2 {
		arcBounds(env, pts[i][0], pts[i][1], pts[i+1][0], pts[i+1][1], pts[i+2][0], pts[i+2][1])
	}
	return nil
}

// arcBounds widens env by the circular arc through p1, p2 and p3. This is
// libgpkg's geom_envelope_fill_arc.
func arcBounds(env *gpkgEnvelope, p1x, p1y, p2x, p2y, p3x, p3y float64) {
	cx, cy := arcCenter(p1x, p1y, p2x, p2y, p3x, p3y)
	radius := math.Hypot(p1x-cx, p1y-cy)
	start := arcPointAngle(cx, cy, p1x, p1y)
	mid := arcPointAngle(cx, cy, p2x, p2y)
	end := arcPointAngle(cx, cy, p3x, p3y)
	sweep := arcSweep(start, mid, end)

	var xMin, yMin, xMax, yMax float64
	if sweep >= 360 || sweep <= -360 {
		xMin, yMin, xMax, yMax = -radius, -radius, radius, radius
	} else {
		xMin = math.Min(p1x, p3x) - cx
		yMin = math.Min(p1y, p3y) - cy
		xMax = math.Max(p1x, p3x) - cx
		yMax = math.Max(p1y, p3y) - cy
		if arcContains(start, sweep, 0) {
			xMin, xMax = math.Min(xMin, radius), math.Max(xMax, radius)
		}
		if arcContains(start, sweep, 90) {
			yMin, yMax = math.Min(yMin, radius), math.Max(yMax, radius)
		}
		if arcContains(start, sweep, 180) {
			xMin, xMax = math.Min(xMin, -radius), math.Max(xMax, -radius)
		}
		if arcContains(start, sweep, 270) {
			yMin, yMax = math.Min(yMin, -radius), math.Max(yMax, -radius)
		}
	}
	minX, minY := cx+xMin, cy+yMin
	env.add(minX, minY)
	env.add(minX+(xMax-xMin), minY+(yMax-yMin))
}

func arcCenter(p1x, p1y, p2x, p2y, p3x, p3y float64) (float64, float64) {
	p1p2 := p1x == p2x && p1y == p2y
	p1p3 := p1x == p3x && p1y == p3y
	p2p3 := p2x == p3x && p2y == p3y
	switch {
	case p1p2 && p1p3 && p2p3:
		return p1x, p1y
	case p1p2:
		return (p1x + p3x) / 2, (p1y + p3y) / 2
	case p1p3 || p2p3:
		return (p1x + p2x) / 2, (p1y + p2y) / 2
	}
	// Intersect the perpendicular bisectors of p1p2 and p2p3.
	x1, y1 := (p1x+p2x)/2, (p1y+p2y)/2
	x2, y2 := x1+(p2y-p1y), y1-(p2x-p1x)
	x3, y3 := (p2x+p3x)/2, (p2y+p3y)/2
	x4, y4 := x3+(p3y-p2y), y3-(p3x-p2x)
	denom := (y2-y1)*(x4-x3) - (x2-x1)*(y4-y3)
	if math.Abs(denom) < 1e-10 {
		return (x2 + x3) / 2, (y2 + y3) / 2
	}
	s := ((x1-x3)*(y4-y3) - (y1-y3)*(x4-x3)) / denom
	return x1 + s*(x2-x1), y1 + s*(y2-y1)
}

// arcPointAngle is the angle in degrees of (x, y) seen from the centre,
// computed as libgpkg does (90° minus the forward azimuth).
func arcPointAngle(cx, cy, x, y float64) float64 {
	azimuth := math.Pi/2 - math.Atan2(y-cy, x-cx)
	if azimuth < 0 {
		azimuth += 2 * math.Pi
	}
	return 90 - azimuth*(180/math.Pi)
}

func normalizeArcAngle(a float64) float64 {
	switch {
	case a <= -180:
		return a + 360
	case a > 180:
		return a - 360
	}
	return a
}

func arcContains(start, sweep, target float64) bool {
	if sweep >= 360 || sweep <= -360 {
		return true
	}
	start = normalizeArcAngle(start)
	end := start + sweep
	t := normalizeArcAngle(target)
	if sweep >= 0 {
		if end > 180 && t < start {
			return t+360 <= end
		}
		return t >= start && t <= end
	}
	if end <= -180 && t >= start {
		return t-360 >= end
	}
	return t >= end && t <= start
}

func arcSweep(start, mid, end float64) float64 {
	if start < 0 {
		start += 360
	}
	if mid < 0 {
		mid += 360
	}
	if end < 0 {
		end += 360
	}
	sweep := end - start
	if start < end {
		if arcContains(start, sweep, mid) {
			return sweep
		}
		return sweep - 360
	}
	if arcContains(start, sweep+360, mid) {
		return sweep + 360
	}
	return sweep
}
