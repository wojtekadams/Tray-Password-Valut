//go:build windows

package main

// span is a column's text area within a row, in pixels from the row's left edge.
type span struct{ start, end int }

// columnsOf splits a row of the given width into the name, login and password
// columns. As on the project's web page, they share the row 1.2 : 1 : 1, with
// padding at the row's ends and gap between neighbours.
func columnsOf(width, padding, gap int) [3]span {
	inner := max(width-2*padding-2*gap, 0)
	login := padding + inner*6/16 + gap
	password := login + inner*5/16 + gap
	return [3]span{{padding, login - gap}, {login, password - gap}, {password, max(width-padding, password)}}
}

// columnAt returns the column at x. A gap belongs to the column on its right.
func columnAt(x int, columns [3]span) int {
	switch {
	case x < columns[0].end:
		return 0
	case x < columns[loginColumn].end:
		return loginColumn
	default:
		return passwordColumn
	}
}
