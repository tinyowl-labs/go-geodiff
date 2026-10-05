package driver

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"math"
	"strings"
	"testing"
)

// wkb builds little-endian ISO WKB from a type code and a body of uint32
// counts, float64 ordinates and nested []byte geometries.
func wkb(code uint32, body ...any) []byte {
	var b bytes.Buffer
	b.WriteByte(1)
	binary.Write(&b, binary.LittleEndian, code)
	for _, v := range body {
		switch v := v.(type) {
		case int:
			binary.Write(&b, binary.LittleEndian, uint32(v))
		case float64:
			binary.Write(&b, binary.LittleEndian, v)
		case []byte:
			b.Write(v)
		}
	}
	return b.Bytes()
}

// gpb wraps WKB in a GeoPackage header with no envelope, so the functions
// must read the geometry itself.
func gpb(body []byte, empty bool) []byte {
	flags := byte(1) // little endian, no envelope
	if empty {
		flags |= 1 << 4
	}
	return append([]byte{'G', 'P', 0, flags, 0, 0, 0, 0}, body...)
}

func TestGpkgFunctions(t *testing.T) {
	registerGpkgFunctions()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	nan := math.NaN()

	withEnvelope := []byte{'G', 'P', 0, 3, 0, 0, 0, 0}
	for _, v := range []float64{-5, 5, -6, 6} {
		withEnvelope = binary.LittleEndian.AppendUint64(withEnvelope, math.Float64bits(v))
	}
	withEnvelope = append(withEnvelope, wkb(2, 2, 0.0, 0.0, 1.0, 1.0)...)

	cases := []struct {
		name  string
		geom  []byte
		empty int64
		env   []float64 // minx, maxx, miny, maxy; nil for NULL
	}{
		{"point", gpb(wkb(1, 3.0, -4.0), false), 0, []float64{3, 3, -4, -4}},
		{"empty point", gpb(wkb(1, nan, nan), true), 1, nil},
		{"line string", gpb(wkb(2, 3, 0.0, 0.0, 10.0, 1.0, -2.0, 5.0), false), 0, []float64{-2, 10, 0, 5}},
		{"empty line string", gpb(wkb(2, 0), true), 1, nil},
		{"line string Z", gpb(wkb(1002, 2, 1.0, 2.0, 3.0, 4.0, 5.0, 6.0), false), 0, []float64{1, 4, 2, 5}},
		{"polygon with hole", gpb(wkb(3, 2,
			5, 0.0, 0.0, 4.0, 0.0, 4.0, 4.0, 0.0, 4.0, 0.0, 0.0,
			4, 1.0, 1.0, 2.0, 1.0, 2.0, 2.0, 1.0, 1.0), false), 0, []float64{0, 4, 0, 4}},
		{"multipoint", gpb(wkb(4, 2, wkb(1, 5.0, 5.0), wkb(1, -3.0, 7.0)), false), 0, []float64{-3, 5, 5, 7}},
		{"collection", gpb(wkb(7, 2, wkb(1, 8.0, 9.0), wkb(2, 2, 0.0, 0.0, 1.0, 1.0)), false), 0, []float64{0, 8, 0, 9}},
		// GDAL's spatial index gives the same bounds for these arcs.
		{"circular string", gpb(wkb(8, 3, 0.0, 0.0, 1.0, 1.0, 2.0, 0.0), false), 0, []float64{0, 2, 0, 1}},
		{"two arcs", gpb(wkb(8, 5, 0.0, 0.0, 1.0, -1.0, 2.0, 0.0, 3.0, 1.0, 4.0, 0.0), false), 0, []float64{0, 4, -1, 1}},
		{"compound curve", gpb(wkb(9, 2, wkb(8, 3, 0.0, 0.0, 1.0, 1.0, 2.0, 0.0), wkb(2, 2, 2.0, 0.0, 3.0, 0.0)), false), 0, []float64{0, 3, 0, 1}},
		{"header envelope wins", withEnvelope, 0, []float64{-5, 5, -6, 6}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var empty int64
			var minX, maxX, minY, maxY sql.NullFloat64
			err := db.QueryRow("SELECT ST_IsEmpty(?1), ST_MinX(?1), ST_MaxX(?1), ST_MinY(?1), ST_MaxY(?1)", c.geom).
				Scan(&empty, &minX, &maxX, &minY, &maxY)
			if err != nil {
				t.Fatal(err)
			}
			if empty != c.empty {
				t.Errorf("ST_IsEmpty = %d, want %d", empty, c.empty)
			}
			got := []sql.NullFloat64{minX, maxX, minY, maxY}
			for i, g := range got {
				if c.env == nil {
					if g.Valid {
						t.Errorf("bound %d = %v, want NULL", i, g.Float64)
					}
				} else if !g.Valid || math.Abs(g.Float64-c.env[i]) > 1e-12 {
					t.Errorf("bound %d = %v, want %v", i, g, c.env[i])
				}
			}
		})
	}

	t.Run("NULL", func(t *testing.T) {
		var empty, minX sql.NullFloat64
		if err := db.QueryRow("SELECT ST_IsEmpty(NULL), ST_MinX(X'')").Scan(&empty, &minX); err != nil {
			t.Fatal(err)
		}
		if empty.Valid || minX.Valid {
			t.Errorf("got %v, %v; want NULL", empty, minX)
		}
	})

	for _, bad := range []struct {
		name string
		geom []byte
		want string
	}{
		{"not a GeoPackage blob", []byte{0, 1, 2, 3}, "Invalid geometry blob header"},
		{"wrong magic", gpb(wkb(1, 0.0, 0.0), false)[1:], "magic"},
		{"short body", gpb(wkb(2, 2, 0.0, 0.0), false), "too short"},
		{"unsupported type", gpb(wkb(15, 0), false), "Unsupported WKB geometry type"},
		{"mixed dimensions", gpb(wkb(4, 1, wkb(1001, 1.0, 2.0, 3.0)), false), "Invalid geometry blob header"},
		{"circular string of two points", gpb(wkb(8, 2, 0.0, 0.0, 1.0, 1.0), false), "CircularString"},
	} {
		t.Run(bad.name, func(t *testing.T) {
			var v any
			err := db.QueryRow("SELECT ST_MinX(?)", bad.geom).Scan(&v)
			if err == nil || !strings.Contains(err.Error(), bad.want) {
				t.Fatalf("err = %v, want %q", err, bad.want)
			}
		})
	}
}
