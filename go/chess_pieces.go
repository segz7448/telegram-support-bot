package main

import (
	"image"
	"image/color"
	"math"
)

// Piece silhouettes drawn from simple polygons and circles written for this bot (our own shapes, no third-party
// artwork, so there is no licence to carry). Coordinates are in a 100x100 cell, y down. Each sprite is rasterised
// at 4x and averaged down for smooth edges, then cached.

type pshape struct {
	polys [][]float64 // x,y pairs
	circs [][3]float64
	cuts  [][]float64 // detail polygons drawn in the edge colour
	cutC  [][3]float64
}

var pbase = []float64{24, 76, 76, 76, 78, 88, 22, 88}

func pieceShape(v int) pshape {
	switch v {
	case int(wP):
		return pshape{polys: [][]float64{{41, 44, 59, 44, 66, 76, 34, 76}, pbase, {36, 54, 64, 54, 62, 60, 38, 60}}, circs: [][3]float64{{50, 31, 13}}}
	case int(wR):
		return pshape{polys: [][]float64{{28, 16, 38, 16, 38, 24, 45, 24, 45, 16, 55, 16, 55, 24, 62, 24, 62, 16, 72, 16, 72, 38, 65, 43, 67, 74, 33, 74, 35, 43, 28, 38}, {24, 72, 76, 72, 78, 88, 22, 88}}}
	case int(wB):
		return pshape{polys: [][]float64{{50, 16, 62, 30, 62, 42, 56, 50, 66, 76, 34, 76, 44, 50, 38, 42, 38, 30}, {24, 74, 76, 74, 78, 88, 22, 88}}, circs: [][3]float64{{50, 13, 5}}, cuts: [][]float64{{51, 23, 54, 26, 47, 41, 44, 38}}}
	case int(wN):
		return pshape{polys: [][]float64{{30, 76, 33, 56, 43, 44, 37, 38, 25, 45, 21, 38, 34, 24, 40, 12, 46, 20, 54, 14, 64, 24, 72, 44, 74, 76}, {24, 74, 76, 74, 78, 88, 22, 88}}, cutC: [][3]float64{{50, 29, 2.8}}}
	case int(wQ):
		return pshape{polys: [][]float64{{24, 30, 36, 56, 39, 26, 50, 54, 61, 26, 64, 56, 76, 30, 70, 68, 30, 68}, {30, 66, 70, 66, 74, 78, 26, 78}, {24, 76, 76, 76, 78, 88, 22, 88}}, circs: [][3]float64{{24, 27, 4.5}, {39, 21, 4.5}, {61, 21, 4.5}, {76, 27, 4.5}, {50, 17, 4.5}}}
	case int(wK):
		return pshape{polys: [][]float64{{46, 6, 54, 6, 54, 14, 62, 14, 62, 21, 54, 21, 54, 30, 46, 30, 46, 21, 38, 21, 38, 14, 46, 14}, {30, 46, 38, 32, 62, 32, 70, 46, 66, 68, 34, 68}, {30, 66, 70, 66, 74, 78, 26, 78}, {24, 76, 76, 76, 78, 88, 22, 88}}}
	}
	return pshape{}
}

func inPoly(pts []float64, x, y float64) bool {
	in := false
	n := len(pts) / 2
	for i, j := 0, n-1; i < n; j, i = i, i+1 {
		xi, yi, xj, yj := pts[2*i], pts[2*i+1], pts[2*j], pts[2*j+1]
		if (yi > y) != (yj > y) && x < (xj-xi)*(y-yi)/(yj-yi)+xi {
			in = !in
		}
	}
	return in
}

func (s pshape) inside(x, y float64) bool {
	for _, p := range s.polys {
		if inPoly(p, x, y) {
			return true
		}
	}
	for _, c := range s.circs {
		if math.Hypot(x-c[0], y-c[1]) <= c[2] {
			return true
		}
	}
	return false
}

func (s pshape) isCut(x, y float64) bool {
	for _, p := range s.cuts {
		if inPoly(p, x, y) {
			return true
		}
	}
	for _, c := range s.cutC {
		if math.Hypot(x-c[0], y-c[1]) <= c[2] {
			return true
		}
	}
	return false
}

var spriteCache = map[int]*image.RGBA{}

// sprite returns the size x size piece image for signed piece value v (negative = black).
func sprite(v, size int) *image.RGBA {
	key := v*1000 + size
	if im, ok := spriteCache[key]; ok {
		return im
	}
	const ss = 4
	sh := pieceShape(int(abs8(int8(v))))
	fill, edge := color.RGBA{250, 249, 246, 255}, color.RGBA{28, 28, 30, 255}
	if v < 0 {
		fill, edge = color.RGBA{64, 62, 68, 255}, color.RGBA{238, 236, 232, 255}
	}
	n := size * ss
	scale := 100.0 / float64(n)
	mask := make([]uint8, n*n) // 0 outside, 1 fill, 2 cut
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			fx, fy := 50+((float64(x)+0.5)*scale-50)/1.22, (float64(y)+0.5)*scale
			if sh.inside(fx, fy) {
				mask[y*n+x] = 1
				if sh.isCut(fx, fy) {
					mask[y*n+x] = 2
				}
			}
		}
	}
	rad := ss * 3 / 2 // outline about 1.5px thick
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var r, g, b, a float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					x, y := px*ss+sx, py*ss+sy
					var c color.RGBA
					have := false
					switch mask[y*n+x] {
					case 1:
						// inside: edge if within rad of the outside
						edgeNear := false
						for dy := -rad; dy <= rad && !edgeNear; dy += rad {
							for dx := -rad; dx <= rad; dx += rad {
								xx, yy := x+dx, y+dy
								if xx < 0 || yy < 0 || xx >= n || yy >= n || mask[yy*n+xx] == 0 {
									edgeNear = true
									break
								}
							}
						}
						c, have = fill, true
						if edgeNear {
							c = edge
						}
					case 2:
						c, have = edge, true
					default:
						// outside: dark halo in edge colour just outside the shape
						near := false
						for dy := -rad; dy <= rad && !near; dy += rad {
							for dx := -rad; dx <= rad; dx += rad {
								xx, yy := x+dx, y+dy
								if xx >= 0 && yy >= 0 && xx < n && yy < n && mask[yy*n+xx] != 0 {
									near = true
									break
								}
							}
						}
						if near {
							c, have = edge, true
						}
					}
					if have {
						r += float64(c.R)
						g += float64(c.G)
						b += float64(c.B)
						a++
					}
				}
			}
			if a > 0 {
				img.SetRGBA(px, py, color.RGBA{uint8(r / a), uint8(g / a), uint8(b / a), uint8(a * 255 / (ss * ss))})
			}
		}
	}
	spriteCache[key] = img
	return img
}
