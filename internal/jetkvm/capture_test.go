package jetkvm

import (
	"testing"
)

func TestParseNALUnits(t *testing.T) {
	startCode := []byte{0, 0, 0, 1}

	t.Run("empty input", func(t *testing.T) {
		if got := parseNALUnits(nil); len(got) != 0 {
			t.Fatalf("expected empty, got %d units", len(got))
		}
	})

	t.Run("single unit", func(t *testing.T) {
		data := append(startCode, 0x67, 0xAA, 0xBB)
		units := parseNALUnits(data)
		if len(units) != 1 {
			t.Fatalf("expected 1 unit, got %d", len(units))
		}
		if got := units[0]; string(got) != string(data) {
			t.Fatalf("unit mismatch: %v vs %v", got, data)
		}
	})

	t.Run("two units", func(t *testing.T) {
		unit1 := append(startCode, 0x67, 0x01)
		unit2 := append(startCode, 0x68, 0x02)
		data := append(unit1, unit2...)
		units := parseNALUnits(data)
		if len(units) != 2 {
			t.Fatalf("expected 2 units, got %d", len(units))
		}
	})

	t.Run("three units", func(t *testing.T) {
		build := func(b byte) []byte { return append(append([]byte(nil), startCode...), b) }
		data := append(build(0x67), append(build(0x68), build(0x65)...)...)
		units := parseNALUnits(data)
		if len(units) != 3 {
			t.Fatalf("expected 3 units, got %d", len(units))
		}
	})
}

func TestNALUnitType(t *testing.T) {
	tests := []struct {
		name     string
		unit     []byte
		expected byte
	}{
		{"too short", []byte{0, 0, 0, 1}, 0},
		{"SPS (7)", []byte{0, 0, 0, 1, 0x67}, nalSPS},
		{"PPS (8)", []byte{0, 0, 0, 1, 0x68}, nalPPS},
		{"IDR (5)", []byte{0, 0, 0, 1, 0x65}, nalIDR},
		{"masks top bits", []byte{0, 0, 0, 1, 0xFF}, 0x1F},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := nalUnitType(tc.unit); got != tc.expected {
				t.Fatalf("nalUnitType(%v) = %d, want %d", tc.unit, got, tc.expected)
			}
		})
	}
}
