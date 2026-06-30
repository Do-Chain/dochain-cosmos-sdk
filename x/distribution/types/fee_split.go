package types

import "cosmossdk.io/math"

const (
	// DoChainMainnetChainID is the chain ID where the buyback/liquidity fee split
	// is activated as a coordinated hard fork.
	DoChainMainnetChainID = "Do-Chain"

	// GasFeeSplitUpgradeHeight is the first block that applies the 10/20/70 gas
	// fee split on Do-Chain mainnet.
	GasFeeSplitUpgradeHeight = int64(1_610_000)

	// BuybackLiquidityPoolName is the module account that receives the 20%
	// buyback/liquidity share of collected gas fees.
	BuybackLiquidityPoolName = "buyback_liquidity_pool"
)

// IsBuybackLiquidityFeeSplitActive returns true once Do-Chain mainnet has
// reached the coordinated gas fee split upgrade height.
func IsBuybackLiquidityFeeSplitActive(chainID string, height int64) bool {
	return chainID == DoChainMainnetChainID && height >= GasFeeSplitUpgradeHeight
}

// CommunityPoolFeeShare is the target community pool share of collected gas fees.
func CommunityPoolFeeShare() math.LegacyDec {
	return math.LegacyNewDecWithPrec(1, 1)
}

// BuybackLiquidityPoolFeeShare is the target buyback/liquidity share of collected gas fees.
func BuybackLiquidityPoolFeeShare() math.LegacyDec {
	return math.LegacyNewDecWithPrec(2, 1)
}

// ValidatorRewardsFeeShare is the target validator/delegator rewards share of collected gas fees.
func ValidatorRewardsFeeShare() math.LegacyDec {
	return math.LegacyNewDecWithPrec(7, 1)
}
