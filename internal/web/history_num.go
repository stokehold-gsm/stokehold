package web

import "strconv"

// num prints a chart value: whole numbers without a fraction, big ones rounded.
func num(v float64) string {
	if v == float64(int64(v)) || v >= 100 {
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}
