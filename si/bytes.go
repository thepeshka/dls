package si

import (
	"fmt"
	"strconv"
)

const (
	Byte = 1.0
	Bits = 8.0
)

const (
	Kilo   = 1000.0
	Mega   = Kilo * Kilo
	Giga   = Kilo * Mega
	Tera   = Kilo * Giga
	Peta   = Kilo * Tera
	Exa    = Kilo * Peta
	Yotta  = Kilo * Exa
	Ronna  = Kilo * Yotta
	Quetta = Kilo * Ronna
)

const (
	Kibi  = 1024.0
	Mebi  = Kibi * Kibi
	Gibi  = Kibi * Mebi
	Tebi  = Kibi * Gibi
	Pebi  = Kibi * Tebi
	Exbi  = Kibi * Pebi
	Yobi  = Kibi * Exbi
	Robi  = Kibi * Yobi
	Quebi = Kibi * Robi
)

var Prefixes = []string{"", "K", "M", "G", "T", "P", "E", "Y", "R", "Q"}

var Exponents10 = []float64{Byte, Kilo, Mega, Giga, Tera, Peta, Exa, Yotta, Ronna, Quetta}
var Exponents2 = []float64{Byte, Kibi, Mebi, Gibi, Tebi, Pebi, Exbi, Yobi, Robi, Quebi}

type Bytes float64

func NewBytes(v any) Bytes {
	switch v := v.(type) {
	case float64:
		return Bytes(v)
	case int:
		return NewBytes(float64(v))
	case int16:
		return NewBytes(float64(v))
	case int32:
		return NewBytes(float64(v))
	case int64:
		return NewBytes(float64(v))
	case uint:
		return NewBytes(float64(v))
	case uint16:
		return NewBytes(float64(v))
	case uint32:
		return NewBytes(float64(v))
	case uint64:
		return NewBytes(float64(v))
	case float32:
		return NewBytes(float64(v))
	default:
		panic(fmt.Errorf("cant make %T to bytes", v))
	}
}

type FormatBase float64

var (
	Base10 FormatBase = Kilo
	Base2  FormatBase = Kibi
)

func (f FormatBase) String() string {
	switch f {
	case Kilo:
		return ""
	case Kibi:
		return "i"
	}
	panic("invalid FormatBase")
}

type FormatUnit float64

var (
	UnitBytes FormatUnit = Byte
	UnitBits  FormatUnit = Bits
)

func (f FormatUnit) String() string {
	switch f {
	case Byte:
		return "B"
	case Bits:
		return "b"
	}
	panic("invalid FormatUnit")
}

func convertClosest(v float64, exponents []float64) (float64, string) {
	for i, exp := range exponents {
		if v < exp {
			return v / exponents[i-1], Prefixes[i-1]
		}
	}
	return v / exponents[len(exponents)-1], Prefixes[len(Prefixes)-1]
}

func (b Bytes) FormatBase(base FormatBase, unit FormatUnit) (float64, string) {
	exponents := Exponents10
	if base == Base2 {
		exponents = Exponents2
	}
	v, suffix := convertClosest(float64(b)*float64(unit), exponents)
	return v, suffix + base.String() + unit.String()
}

func (b Bytes) Format(s fmt.State, format rune) {
	base := Base10
	unit := UnitBytes
	var frmt string
	for _, f := range "-+ 0" {
		if s.Flag(int(f)) {
			frmt += string(f)
		}
	}
	if s.Flag(int('#')) {
		unit = UnitBits
	}
	if w, ok := s.Width(); ok {
		if w == 2 {
			base = Base2
		}
	}
	if p, ok := s.Precision(); ok {
		frmt += "." + strconv.Itoa(p)
	}
	frmt += string(format)
	val, suffix := b.FormatBase(base, unit)
	fmt.Fprintf(s, fmt.Sprintf("%%%s %%s", frmt), val, suffix)
}

func (b Bytes) String() string {
	return fmt.Sprintf("%.2f", b)
}
