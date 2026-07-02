package event

import "math"

// 圖形辨識（伺服器權威）：由前端送來的實際筆跡點，重取樣+正規化後比對三角/四角/五芒星，
// 依「品質距離 + 相對次佳的 margin」給分，避免前端直接送分數刷獎、也避免三角/四角互相誤判。
type ptf struct{ X, Y float64 }

func shapePathGo(shape int) []ptf {
	poly := func(n int, startDeg float64) []ptf {
		v := make([]ptf, 0, n)
		for k := 0; k < n; k++ {
			a := (startDeg + float64(k)*360/float64(n)) * math.Pi / 180
			v = append(v, ptf{math.Cos(a), math.Sin(a)})
		}
		return v
	}
	if shape == 5 {
		o := poly(5, -90)
		path := []ptf{o[0], o[2], o[4], o[1], o[3]}
		return append(path, path[0])
	}
	n, start := 3, -90.0
	if shape == 4 {
		n, start = 4, -135.0
	}
	v := poly(n, start)
	return append(v, v[0])
}

func pathLenGo(pts []ptf) float64 {
	L := 0.0
	for i := 1; i < len(pts); i++ {
		L += math.Hypot(pts[i].X-pts[i-1].X, pts[i].Y-pts[i-1].Y)
	}
	return L
}

func resampleGo(pts []ptf, n int) []ptf {
	if len(pts) < 2 {
		var p ptf
		if len(pts) == 1 {
			p = pts[0]
		}
		out := make([]ptf, n)
		for i := range out {
			out[i] = p
		}
		return out
	}
	I := pathLenGo(pts) / float64(n-1)
	src := append([]ptf(nil), pts...)
	out := []ptf{src[0]}
	D := 0.0
	for i := 1; i < len(src); i++ {
		d := math.Hypot(src[i].X-src[i-1].X, src[i].Y-src[i-1].Y)
		if D+d >= I && d > 0 {
			t := (I - D) / d
			q := ptf{src[i-1].X + t*(src[i].X-src[i-1].X), src[i-1].Y + t*(src[i].Y-src[i-1].Y)}
			out = append(out, q)
			src = append(src[:i], append([]ptf{q}, src[i:]...)...)
			D = 0
		} else {
			D += d
		}
	}
	for len(out) < n {
		out = append(out, src[len(src)-1])
	}
	return out[:n]
}

func normalizeGo(pts []ptf) []ptf {
	var cx, cy float64
	for _, p := range pts {
		cx += p.X
		cy += p.Y
	}
	cx /= float64(len(pts))
	cy /= float64(len(pts))
	cen := make([]ptf, len(pts))
	var s float64
	for i, p := range pts {
		cen[i] = ptf{p.X - cx, p.Y - cy}
		s += cen[i].X*cen[i].X + cen[i].Y*cen[i].Y
	}
	s = math.Sqrt(s / float64(len(pts)))
	if s < 1e-6 {
		s = 1e-6
	}
	for i := range cen {
		cen[i].X /= s
		cen[i].Y /= s
	}
	return cen
}

const shapeN = 48

func shapeMatchDistGo(drawn []ptf, shape int) float64 {
	a := normalizeGo(resampleGo(drawn, shapeN))
	b := normalizeGo(resampleGo(shapePathGo(shape), shapeN))
	best := math.Inf(1)
	for _, rev := range []bool{false, true} {
		bb := b
		if rev {
			bb = make([]ptf, shapeN)
			for i := 0; i < shapeN; i++ {
				bb[i] = b[shapeN-1-i]
			}
		}
		for sft := 0; sft < shapeN; sft++ {
			sum := 0.0
			for i := 0; i < shapeN; i++ {
				p, q := a[i], bb[(i+sft)%shapeN]
				sum += math.Hypot(p.X-q.X, p.Y-q.Y)
			}
			if avg := sum / float64(shapeN); avg < best {
				best = avg
			}
		}
	}
	return best
}

// shapeDegree 由筆跡點回傳 0..1：目標圖形須為最接近、且領先次佳 >=0.05 margin，再依距離分級。
func shapeDegree(pts [][2]float64, shape int) float64 {
	if len(pts) < 8 || len(pts) > 5000 || (shape != 3 && shape != 4 && shape != 5) {
		return 0
	}
	drawn := make([]ptf, len(pts))
	for i, p := range pts {
		drawn[i] = ptf{p[0], p[1]}
	}
	best, second := math.Inf(1), math.Inf(1)
	bestShape := 0
	dTarget := math.Inf(1)
	for _, s := range []int{3, 4, 5} {
		d := shapeMatchDistGo(drawn, s)
		if s == shape {
			dTarget = d
		}
		if d < best {
			second, best, bestShape = best, d, s
		} else if d < second {
			second = d
		}
	}
	if bestShape != shape || second-dTarget < 0.05 {
		return 0
	}
	switch {
	case dTarget <= 0.12:
		return 1.0
	case dTarget <= 0.18:
		return 0.6
	case dTarget <= 0.24:
		return 0.3
	}
	return 0
}
