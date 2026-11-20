package stats

import "math/big"

// UsageCostText 将万分之一度和万分之一元/度的乘积四舍五入到分，避免整数溢出和浮点误差。
func UsageCostText(usage, unitPrice int64) string {
	product := new(big.Int).Mul(big.NewInt(usage), big.NewInt(unitPrice))
	return new(big.Rat).SetFrac(product, big.NewInt(100000000)).FloatString(2)
}
