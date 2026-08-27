// Package units formats resource quantities the way kubectl does, so numbers
// in a capsize report can be pasted straight into a manifest.
package units

import (
	"fmt"
	"math"
	"strconv"
)

const (
	Ki = int64(1) << 10
	Mi = int64(1) << 20
	Gi = int64(1) << 30
	Ti = int64(1) << 40
)

// Bytes renders a byte count in binary SI, the notation Kubernetes accepts.
func Bytes(n int64) string {
	switch {
	case n == 0:
		return "0"
	case n < 0:
		return "-" + Bytes(-n)
	case n >= Ti:
		return trim(float64(n)/float64(Ti)) + "Ti"
	case n >= Gi:
		return trim(float64(n)/float64(Gi)) + "Gi"
	case n >= Mi:
		return trim(float64(n)/float64(Mi)) + "Mi"
	case n >= Ki:
		return trim(float64(n)/float64(Ki)) + "Ki"
	default:
		return strconv.FormatInt(n, 10)
	}
}

// CPU renders millicores as kubectl would: "500m", or "2" for whole cores.
func CPU(milli int64) string {
	if milli == 0 {
		return "0"
	}
	if milli%1000 == 0 {
		return strconv.FormatInt(milli/1000, 10)
	}
	return strconv.FormatInt(milli, 10) + "m"
}

// RoundUpMi rounds a byte count up to a whole mebibyte. Recommendations are
// rounded up, never down: the rounding error should land on the side that
// keeps the workload alive.
func RoundUpMi(n int64) int64 {
	if n <= 0 {
		return 0
	}
	return ((n + Mi - 1) / Mi) * Mi
}

// RoundUpMilli rounds millicores up to the nearest 10m.
func RoundUpMilli(n int64) int64 {
	if n <= 0 {
		return 0
	}
	return ((n + 9) / 10) * 10
}

// Ratio renders a multiplier compactly: "4.0x", "12x", ">1000x".
func Ratio(f float64) string {
	switch {
	case math.IsInf(f, 0) || math.IsNaN(f):
		return "n/a"
	case f >= 1000:
		return ">1000x"
	case f >= 10:
		return fmt.Sprintf("%.0fx", f)
	default:
		return fmt.Sprintf("%.1fx", f)
	}
}

// Score renders a risk score with a precision that stays readable across the
// four orders of magnitude these scores actually span.
func Score(f float64) string {
	switch {
	case math.IsInf(f, 0) || math.IsNaN(f):
		return "n/a"
	case f == 0:
		return "0"
	case f >= 1000:
		return fmt.Sprintf("%.0f", f)
	case f >= 10:
		return fmt.Sprintf("%.1f", f)
	default:
		return fmt.Sprintf("%.2f", f)
	}
}

func trim(f float64) string {
	s := strconv.FormatFloat(f, 'f', 1, 64)
	if len(s) > 2 && s[len(s)-2:] == ".0" {
		return s[:len(s)-2]
	}
	return s
}
