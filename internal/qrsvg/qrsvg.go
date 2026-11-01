// Package qrsvg draws a QR code as inline SVG, so pages can show one without
// images from elsewhere (the panels' CSP allows no remote images).
package qrsvg

import (
	"fmt"
	"html/template"
	"strings"

	"rsc.io/qr"
)

// SVG returns the code for text with a quiet zone, or "" if text does not fit.
func SVG(text string) template.HTML {
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return ""
	}
	const quiet = 4
	n := c.Size + 2*quiet
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="qr" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges" role="img" aria-label="QR"><rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, n, n, n, n)
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; x++ {
			if c.Black(x, y) {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+quiet, y+quiet)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return template.HTML(b.String()) // built only from numbers above, no user text inside
}
