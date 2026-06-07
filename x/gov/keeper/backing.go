package keeper

import (
	"context"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/errors"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/gov/types"
	v1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

const (
	proposalBackingThresholdNumerator   uint64 = 33
	proposalBackingThresholdDenominator uint64 = 100
)

// AddProposalBacking records the validator-stage vote for a proposal. During
// this phase, each bonded self-delegated validator counts once regardless of
// stake, and 33% YES support moves the proposal to the public voting period.
func (k Keeper) AddProposalBacking(ctx context.Context, proposalID uint64, backer sdk.AccAddress, options v1.WeightedVoteOptions, metadata string) (bool, error) {
	proposal, err := k.Proposals.Get(ctx, proposalID)
	if err != nil {
		return false, err
	}

	if proposal.Status != v1.StatusDepositPeriod {
		return false, errors.Wrapf(types.ErrInactiveProposal, "%d", proposalID)
	}

	if err := k.assertMetadataLength(metadata); err != nil {
		return false, err
	}

	option, ok := singleYesNoVoteOption(options)
	if !ok {
		return false, errors.Wrap(types.ErrInvalidVote, "validator-stage voting only accepts a single YES or NO vote")
	}

	isValidator, selfDelegated, err := k.isBondedValidatorSelfDelegated(ctx, backer)
	if err != nil {
		return false, err
	}
	if !isValidator {
		return false, errors.Wrap(types.ErrInvalidVote, "phase-one backing requires a bonded validator operator")
	}
	if !selfDelegated {
		return false, errors.Wrap(types.ErrInvalidVote, "phase-one backing requires validator self-delegation")
	}

	if err := k.ProposalBackers.Set(ctx, collections.Join(proposalID, backer), []byte{byte(option)}); err != nil {
		return false, err
	}

	backingCount, bondedValidatorCount, err := k.proposalBackingCounts(ctx, proposalID)
	if err != nil {
		return false, err
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	sdkCtx.EventManager().EmitEvent(
		sdk.NewEvent(
			types.EventTypeProposalBacking,
			sdk.NewAttribute(types.AttributeKeyBacker, backer.String()),
			sdk.NewAttribute(types.AttributeKeyOption, option.String()),
			sdk.NewAttribute(types.AttributeKeyProposalID, fmt.Sprintf("%d", proposalID)),
			sdk.NewAttribute(types.AttributeKeyBackingCount, fmt.Sprintf("%d", backingCount)),
			sdk.NewAttribute(types.AttributeKeyBondedValidatorCount, fmt.Sprintf("%d", bondedValidatorCount)),
		),
	)

	return k.TryActivateVotingPeriod(ctx, proposal)
}

// TryActivateVotingPeriod starts phase two once the proposal has both its
// required deposit and 33% YES support from bonded validators.
func (k Keeper) TryActivateVotingPeriod(ctx context.Context, proposal v1.Proposal) (bool, error) {
	if proposal.Status != v1.StatusDepositPeriod {
		return false, nil
	}

	hasDeposit, err := k.proposalHasMinDeposit(ctx, proposal)
	if err != nil {
		return false, err
	}
	if !hasDeposit {
		return false, nil
	}

	hasBacking, err := k.proposalBackingThresholdMet(ctx, proposal.Id)
	if err != nil {
		return false, err
	}
	if !hasBacking {
		return false, nil
	}

	if err := k.ActivateVotingPeriod(ctx, proposal); err != nil {
		return false, err
	}

	return true, nil
}

func (k Keeper) proposalHasMinDeposit(ctx context.Context, proposal v1.Proposal) (bool, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return false, err
	}

	minDepositAmount := proposal.GetMinDepositFromParams(params)
	return sdk.NewCoins(proposal.TotalDeposit...).IsAllGTE(minDepositAmount), nil
}

func (k Keeper) proposalBackingThresholdMet(ctx context.Context, proposalID uint64) (bool, error) {
	backingCount, bondedValidatorCount, err := k.proposalBackingCounts(ctx, proposalID)
	if err != nil {
		return false, err
	}

	if bondedValidatorCount == 0 {
		return false, nil
	}

	return backingCount*proposalBackingThresholdDenominator >= bondedValidatorCount*proposalBackingThresholdNumerator, nil
}

func (k Keeper) proposalBackingCounts(ctx context.Context, proposalID uint64) (backingCount, bondedValidatorCount uint64, err error) {
	rng := collections.NewPrefixedPairRange[uint64, sdk.AccAddress](proposalID)
	err = k.ProposalBackers.Walk(ctx, rng, func(_ collections.Pair[uint64, sdk.AccAddress], option []byte) (bool, error) {
		if len(option) == 1 && v1.VoteOption(option[0]) == v1.OptionYes {
			backingCount++
		}
		return false, nil
	})
	if err != nil {
		return 0, 0, err
	}

	bondedValidatorCount, err = k.bondedValidatorCount(ctx)
	if err != nil {
		return 0, 0, err
	}

	return backingCount, bondedValidatorCount, nil
}

func (k Keeper) bondedValidatorCount(ctx context.Context) (uint64, error) {
	var count uint64
	err := k.sk.IterateBondedValidatorsByPower(ctx, func(_ int64, _ stakingtypes.ValidatorI) bool {
		count++
		return false
	})

	return count, err
}

func (k Keeper) isBondedValidator(ctx context.Context, addr sdk.AccAddress) (bool, error) {
	valAddrStr, err := k.sk.ValidatorAddressCodec().BytesToString(addr)
	if err != nil {
		return false, err
	}

	var found bool
	err = k.sk.IterateBondedValidatorsByPower(ctx, func(_ int64, validator stakingtypes.ValidatorI) bool {
		if validator.GetOperator() == valAddrStr {
			found = true
			return true
		}
		return false
	})
	if err != nil {
		return false, err
	}

	return found, nil
}

func (k Keeper) isBondedValidatorSelfDelegated(ctx context.Context, addr sdk.AccAddress) (bool, bool, error) {
	valAddrStr, err := k.sk.ValidatorAddressCodec().BytesToString(addr)
	if err != nil {
		return false, false, err
	}

	isValidator, err := k.isBondedValidator(ctx, addr)
	if err != nil {
		return false, false, err
	}
	if !isValidator {
		return false, false, nil
	}

	selfDelegated := false
	err = k.sk.IterateDelegations(ctx, addr, func(_ int64, delegation stakingtypes.DelegationI) bool {
		if delegation.GetValidatorAddr() == valAddrStr && delegation.GetShares().GT(math.LegacyZeroDec()) {
			selfDelegated = true
			return true
		}
		return false
	})
	if err != nil {
		return false, false, err
	}

	return true, selfDelegated, nil
}

func (k Keeper) deleteProposalBackers(ctx context.Context, proposalID uint64) error {
	rng := collections.NewPrefixedPairRange[uint64, sdk.AccAddress](proposalID)
	return k.ProposalBackers.Clear(ctx, rng)
}

func singleYesNoVoteOption(options v1.WeightedVoteOptions) (v1.VoteOption, bool) {
	if len(options) != 1 || options[0] == nil {
		return v1.OptionEmpty, false
	}

	if options[0].Option != v1.OptionYes && options[0].Option != v1.OptionNo {
		return v1.OptionEmpty, false
	}

	weight, err := math.LegacyNewDecFromStr(options[0].Weight)
	if err != nil {
		return v1.OptionEmpty, false
	}

	return options[0].Option, weight.Equal(math.LegacyOneDec())
}
