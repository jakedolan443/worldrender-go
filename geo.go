package main

import (
	"fmt"
	"math"
	"strings"

	"github.com/jonas-p/go-shp"
)

type Point struct{ X, Y float64 }

type BoundingBox struct{ MinX, MinY, MaxX, MaxY float64 }

type Polygon struct {
	WorldPoints []Point
	Bounds      BoundingBox
	IsHole      bool
}

type CountryPolygon struct {
	Polygon
	Name      string
	Subregion string
}

func ComputeBounds(pts []Point) BoundingBox {
	bb := BoundingBox{math.MaxFloat64, math.MaxFloat64, -math.MaxFloat64, -math.MaxFloat64}
	for _, pt := range pts {
		bb.MinX = math.Min(bb.MinX, pt.X)
		bb.MaxX = math.Max(bb.MaxX, pt.X)
		bb.MinY = math.Min(bb.MinY, pt.Y)
		bb.MaxY = math.Max(bb.MaxY, pt.Y)
	}
	return bb
}

// IsHoleRing returns true if the ring has clockwise winding using the shoelace formula.
func IsHoleRing(pts []Point) bool {
	area := 0.0
	n := len(pts)
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		area += pts[i].X*pts[j].Y - pts[j].X*pts[i].Y
	}
	return area > 0
}

// SubdivideByGrid clips a polygon to a grid of the given cell size.
func SubdivideByGrid(pts []Point, cellSize float64) [][]Point {
	bb := ComputeBounds(pts)
	sx := math.Floor(bb.MinX/cellSize) * cellSize
	ex := math.Ceil(bb.MaxX/cellSize) * cellSize
	sy := math.Floor(bb.MinY/cellSize) * cellSize
	ey := math.Ceil(bb.MaxY/cellSize) * cellSize

	var result [][]Point
	for y := sy; y < ey; y += cellSize {
		for x := sx; x < ex; x += cellSize {
			if c := clipToRect(pts, x, x+cellSize, y, y+cellSize); len(c) >= 3 {
				result = append(result, c)
			}
		}
	}
	return result
}

// clipToRect clips a polygon to a rectangle using the Sutherland-Hodgman algorithm.
func clipToRect(pts []Point, minX, maxX, minY, maxY float64) []Point {
	c := clipEdge(pts,
		func(p Point) bool { return p.Y >= minY },
		func(a, b Point) Point { t := (minY - a.Y) / (b.Y - a.Y); return Point{a.X + t*(b.X-a.X), minY} })
	c = clipEdge(c,
		func(p Point) bool { return p.Y <= maxY },
		func(a, b Point) Point { t := (maxY - a.Y) / (b.Y - a.Y); return Point{a.X + t*(b.X-a.X), maxY} })
	c = clipEdge(c,
		func(p Point) bool { return p.X >= minX },
		func(a, b Point) Point { t := (minX - a.X) / (b.X - a.X); return Point{minX, a.Y + t*(b.Y-a.Y)} })
	c = clipEdge(c,
		func(p Point) bool { return p.X <= maxX },
		func(a, b Point) Point { t := (maxX - a.X) / (b.X - a.X); return Point{maxX, a.Y + t*(b.Y-a.Y)} })
	return c
}

func clipEdge(pts []Point, inside func(Point) bool, intersect func(Point, Point) Point) []Point {
	if len(pts) == 0 {
		return nil
	}
	var out []Point
	n := len(pts)
	for i := 0; i < n; i++ {
		cur, nxt := pts[i], pts[(i+1)%n]
		cIn, nIn := inside(cur), inside(nxt)
		if cIn {
			out = append(out, cur)
			if !nIn {
				out = append(out, intersect(cur, nxt))
			}
		} else if nIn {
			out = append(out, intersect(cur, nxt))
		}
	}
	return out
}

// maxPointsForDirectLoad is the threshold above which polygons are subdivided by grid.
const maxPointsForDirectLoad = 1000000

func LoadShapefile(path string) ([]Polygon, error) {
	shape, err := shp.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open shapefile: %w", err)
	}
	defer shape.Close()

	var polygons []Polygon
	for shape.Next() {
		_, p := shape.Shape()
		s, ok := p.(*shp.Polygon)
		if !ok {
			continue
		}
		for pi := 0; pi < len(s.Parts); pi++ {
			start := s.Parts[pi]
			end := s.NumPoints
			if pi < len(s.Parts)-1 {
				end = s.Parts[pi+1]
			}

			var pts []Point
			for i := start; i < end; i++ {
				pts = append(pts, Point{s.Points[i].X, s.Points[i].Y})
			}
			if len(pts) < 3 {
				continue
			}

			hole := IsHoleRing(pts)
			if len(pts) > maxPointsForDirectLoad {
				for _, sub := range SubdivideByGrid(pts, 30.0) {
					if len(sub) >= 3 {
						polygons = append(polygons, Polygon{
							WorldPoints: sub,
							Bounds:      ComputeBounds(sub),
							IsHole:      hole,
						})
					}
				}
			} else {
				polygons = append(polygons, Polygon{
					WorldPoints: pts,
					Bounds:      ComputeBounds(pts),
					IsHole:      hole,
				})
			}
		}
	}
	return polygons, nil
}

// LoadCountryShapefile reads country polygons without subdividing, to avoid grid lines on borders.
func LoadCountryShapefile(path string) ([]CountryPolygon, error) {
	shape, err := shp.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open shapefile: %w", err)
	}
	defer shape.Close()

	nameIdx, subregIdx := -1, -1
	for i, f := range shape.Fields() {
		switch f.String() {
		case "NAME":
			nameIdx = i
		case "SUBREGION":
			subregIdx = i
		}
	}
	if nameIdx < 0 {
		return nil, fmt.Errorf("no NAME field in shapefile")
	}

	var out []CountryPolygon
	for shape.Next() {
		_, p := shape.Shape()
		s, ok := p.(*shp.Polygon)
		if !ok {
			continue
		}
		name := strings.TrimRight(shape.Attribute(nameIdx), "\x00 ")
		var subregion string
		if subregIdx >= 0 {
			subregion = strings.TrimRight(shape.Attribute(subregIdx), "\x00 ")
		}

		for pi := 0; pi < len(s.Parts); pi++ {
			start := s.Parts[pi]
			end := s.NumPoints
			if pi < len(s.Parts)-1 {
				end = s.Parts[pi+1]
			}

			var pts []Point
			for i := start; i < end; i++ {
				pts = append(pts, Point{s.Points[i].X, s.Points[i].Y})
			}
			if len(pts) < 3 {
				continue
			}

			out = append(out, CountryPolygon{
				Polygon:   Polygon{WorldPoints: pts, Bounds: ComputeBounds(pts), IsHole: IsHoleRing(pts)},
				Name:      name,
				Subregion: subregion,
			})
		}
	}
	return out, nil
}
