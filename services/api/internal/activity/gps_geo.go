package activity

import (
	"math"
	"strings"
)

// --- 軌跡壓縮：Douglas-Peucker 簡化 + Google encoded polyline ---

// simplifyPath 以 Douglas-Peucker 簡化軌跡（epsMeters 公尺容差）
func simplifyPath(pts [][2]float64, epsMeters float64) [][2]float64 {
	if len(pts) < 3 {
		return pts
	}
	return dp(pts, epsMeters)
}

func dp(pts [][2]float64, eps float64) [][2]float64 {
	if len(pts) < 3 {
		return pts
	}
	first, last := pts[0], pts[len(pts)-1]
	maxD, idx := 0.0, 0
	for i := 1; i < len(pts)-1; i++ {
		if d := perpDistM(pts[i], first, last); d > maxD {
			maxD, idx = d, i
		}
	}
	if maxD > eps {
		left := dp(pts[:idx+1], eps)
		right := dp(pts[idx:], eps)
		return append(left[:len(left)-1], right...)
	}
	return [][2]float64{first, last}
}

// perpDistM 點 p 到線段 a-b 的垂直距離（公尺，局部等距投影）
func perpDistM(p, a, b [2]float64) float64 {
	latRad := a[0] * math.Pi / 180
	mLat := 111320.0
	mLng := 111320.0 * math.Cos(latRad)
	px := (p[1] - a[1]) * mLng
	py := (p[0] - a[0]) * mLat
	bx := (b[1] - a[1]) * mLng
	by := (b[0] - a[0]) * mLat
	l2 := bx*bx + by*by
	if l2 == 0 {
		return math.Hypot(px, py)
	}
	t := (px*bx + py*by) / l2
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return math.Hypot(px-t*bx, py-t*by)
}

// encodePolyline 將 [lat,lng] 陣列編成 Google encoded polyline（精度 1e5）
func encodePolyline(pts [][2]float64) string {
	var sb strings.Builder
	var prevLat, prevLng int
	for _, p := range pts {
		lat := int(math.Round(p[0] * 1e5))
		lng := int(math.Round(p[1] * 1e5))
		encodeSigned(&sb, lat-prevLat)
		encodeSigned(&sb, lng-prevLng)
		prevLat, prevLng = lat, lng
	}
	return sb.String()
}

func encodeSigned(sb *strings.Builder, v int) {
	sv := v << 1
	if v < 0 {
		sv = ^sv
	}
	for sv >= 0x20 {
		sb.WriteByte(byte((0x20 | (sv & 0x1f)) + 63))
		sv >>= 5
	}
	sb.WriteByte(byte(sv + 63))
}
