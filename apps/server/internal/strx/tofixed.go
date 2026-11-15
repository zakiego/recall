package strx

import "math/big"

// ToFixed is Number.prototype.toFixed: the exact value of x rounded to digits
// decimals, a tie rounding away from zero (1.25 -> "1.3", where Go's 'f'
// rounds to even). chat and attachments keep their own copies.
func ToFixed(x float64, digits int) string {
	r := new(big.Rat).SetFloat64(x)
	scale := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil))
	r.Mul(r, scale)
	neg := r.Sign() < 0
	if neg {
		r.Neg(r)
	}
	r.Add(r, big.NewRat(1, 2))
	n := new(big.Int).Quo(r.Num(), r.Denom())
	s := n.String()
	if digits > 0 {
		for len(s) <= digits {
			s = "0" + s
		}
		s = s[:len(s)-digits] + "." + s[len(s)-digits:]
	}
	if neg && n.Sign() != 0 {
		s = "-" + s
	}
	return s
}
