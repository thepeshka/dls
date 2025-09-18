package si

import (
	"fmt"
	"math/big"
	"strconv"
)

var Prefixes = []string{"K", "M", "G", "T", "P", "E", "Z", "Y", "R", "Q"}

type Bytes struct {
	f *big.Float
}

func NewBytes(v any) *Bytes {
	switch v := v.(type) {
	case *big.Float:
		return &Bytes{f: v}
	case *big.Int:
		f64, _ := v.Float64()
		return NewBytes(f64)
	case float64:
		return NewBytes(big.NewFloat(v))
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
	Base10 FormatBase = 1000.0
	Base2  FormatBase = 1024.0
)

type FormatUnit string

var (
	UnitBytes FormatUnit = "B"
	UnitBit   FormatUnit = "b"
)

func (b *Bytes) FormatBase(base FormatBase, suffix FormatUnit) (float64, string) {
	base_ := float64(base)
	suffix_ := string(suffix)
	if base == Base2 {
		suffix_ = "i" + suffix_
	}
	v := b.f
	if v.Cmp(big.NewFloat(base_)) < 0 {
		f, _ := v.Float64()
		return f, suffix_
	}
	for _, c := range Prefixes {
		v = v.Quo(v, big.NewFloat(base_))
		if v.Cmp(big.NewFloat(base_)) < 0 {
			f, _ := v.Float64()
			return f, c + suffix_
		}
	}
	f, _ := v.Float64()
	return f, Prefixes[len(Prefixes)-1] + suffix_
}

func (b *Bytes) Format(s fmt.State, format rune) {
	val, suffix := b.FormatBase(Base10, UnitBytes)
	var frmt string
	for _, f := range "-=# @" {
		if s.Flag(int(f)) {
			frmt += string(f)
		}
	}
	if w, ok := s.Width(); ok {
		frmt += strconv.Itoa(w)
	}
	if p, ok := s.Precision(); ok {
		frmt += "." + strconv.Itoa(p)
	}
	frmt += string(format)
	fmt.Fprintf(s, fmt.Sprintf("%%%s %%s", frmt), val, suffix)
}

func (b *Bytes) String() string {
	val, suffix := b.FormatBase(Base10, UnitBytes)
	return fmt.Sprintf("%.2f %s", val, suffix)
}
