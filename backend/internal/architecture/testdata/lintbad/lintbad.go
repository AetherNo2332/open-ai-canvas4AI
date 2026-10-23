package lintbad

func Bad(x int) int {
	y := 1
	y = 2       // ineffassign: initial write has no reader
	if x == x { // staticcheck SA4000: identical expressions
		return y
	}
	return 0
}
