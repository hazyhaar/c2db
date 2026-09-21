package csgfixtures

func SwitchDense(op byte, a, b int64) int64 {
	switch op {
	case 0:
		return a + b
	case 1:
		return a - b
	case 2:
		return a * b
	case 3:
		return a &^ b
	case 4:
		return a & b
	case 5:
		return a | b
	case 6:
		return a ^ b
	case 7:
		return a << 1
	case 8:
		return a >> 1
	case 9:
		return a + 1
	case 10:
		return b + 1
	case 11:
		return a - 1
	case 12:
		return b - 1
	case 13:
		return -a
	case 14:
		return ^a
	case 15:
		return a % 7
	}
	return 0
}

func SwitchHoles(op byte, a, b int64) int64 {
	switch op {
	case 0:
		return a + b
	case 4:
		return a - b
	case 8:
		return a * b
	case 12:
		return a &^ b
	case 16:
		return a & b
	case 20:
		return a | b
	case 24:
		return a ^ b
	case 28:
		return a << 1
	case 32:
		return a >> 1
	case 36:
		return a + 1
	case 40:
		return b + 1
	case 44:
		return a - 1
	case 48:
		return b - 1
	case 52:
		return -a
	case 56:
		return ^a
	case 60:
		return a % 7
	}
	return 0
}

func SwitchSparse(op int, a, b int64) int64 {
	switch op {
	case 0:
		return a + b
	case 20:
		return a - b
	case 40:
		return a * b
	case 60:
		return a &^ b
	case 80:
		return a & b
	case 100:
		return a | b
	case 120:
		return a ^ b
	case 140:
		return a << 1
	case 160:
		return a >> 1
	case 180:
		return a + 1
	case 200:
		return b + 1
	case 220:
		return a - 1
	case 240:
		return b - 1
	case 260:
		return -a
	case 280:
		return ^a
	case 300:
		return a % 7
	}
	return 0
}

func SwitchTooFew(op byte, a, b int64) int64 {
	switch op {
	case 0:
		return a + b
	case 1:
		return a - b
	case 2:
		return a * b
	case 3:
		return a &^ b
	case 4:
		return a & b
	case 5:
		return a | b
	case 6:
		return a ^ b
	}
	return 0
}
