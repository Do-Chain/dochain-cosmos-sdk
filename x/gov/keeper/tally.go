package keeper

import (
	"context"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	v1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

const (
	voterPowerCapNumerator   int64 = 25
	voterPowerCapDenominator int64 = 1000
)

// CalculateVoteResultsAndVotingPowerFn is a function signature for calculating vote results and voting power
// It can be overridden to customize the voting power calculation for proposals
// It gets the proposal tallied and the validators governance infos (bonded tokens, voting power, etc.)
// It must return the total voting power and the results of the vote
type CalculateVoteResultsAndVotingPowerFn func(
	ctx context.Context,
	k Keeper,
	proposal v1.Proposal,
	validators map[string]v1.ValidatorGovInfo,
) (totalVoterPower math.LegacyDec, results map[v1.VoteOption]math.LegacyDec, err error)

func defaultCalculateVoteResultsAndVotingPower(
	ctx context.Context,
	k Keeper,
	proposal v1.Proposal,
	validators map[string]v1.ValidatorGovInfo,
) (totalVoterPower math.LegacyDec, results map[v1.VoteOption]math.LegacyDec, err error) {
	totalVotingPower := math.LegacyZeroDec()

	results = make(map[v1.VoteOption]math.LegacyDec)
	results[v1.OptionYes] = math.LegacyZeroDec()
	results[v1.OptionAbstain] = math.LegacyZeroDec()
	results[v1.OptionNo] = math.LegacyZeroDec()
	results[v1.OptionNoWithVeto] = math.LegacyZeroDec()

	totalBonded, err := k.sk.TotalBondedTokens(ctx)
	if err != nil {
		return math.LegacyZeroDec(), nil, err
	}
	voterPowerCap := math.LegacyNewDecFromInt(totalBonded).
		MulInt64(voterPowerCapNumerator).
		QuoInt64(voterPowerCapDenominator)

	rng := collections.NewPrefixedPairRange[uint64, sdk.AccAddress](proposal.Id)
	votesToRemove := []collections.Pair[uint64, sdk.AccAddress]{}
	err = k.Votes.Walk(ctx, rng, func(key collections.Pair[uint64, sdk.AccAddress], vote v1.Vote) (bool, error) {
		voter, err := k.authKeeper.AddressCodec().StringToBytes(vote.Voter)
		if err != nil {
			return false, err
		}

		valAddrStr, err := k.sk.ValidatorAddressCodec().BytesToString(voter)
		if err != nil {
			return false, err
		}

		// DoChain Community voting excludes validator operators. Validators
		// only participate in phase-one backing, where each validator counts once.
		if _, ok := validators[valAddrStr]; ok {
			votesToRemove = append(votesToRemove, key)
			return false, nil
		}

		voterVotingPower := math.LegacyZeroDec()
		err = k.sk.IterateDelegations(ctx, voter, func(index int64, delegation stakingtypes.DelegationI) (stop bool) {
			valAddrStr := delegation.GetValidatorAddr()

			if val, ok := validators[valAddrStr]; ok {
				// delegation shares * bonded / total shares
				votingPower := delegation.GetShares().MulInt(val.BondedTokens).Quo(val.DelegatorShares)
				voterVotingPower = voterVotingPower.Add(votingPower)
			}

			return false
		})
		if err != nil {
			return false, err
		}

		votingPower := capVoterPower(voterVotingPower, voterPowerCap)
		for _, option := range vote.Options {
			weight, _ := math.LegacyNewDecFromStr(option.Weight)
			subPower := votingPower.Mul(weight)
			results[option.Option] = results[option.Option].Add(subPower)
		}
		totalVotingPower = totalVotingPower.Add(votingPower)

		votesToRemove = append(votesToRemove, key)
		return false, nil
	})
	if err != nil {
		return math.LegacyZeroDec(), nil, fmt.Errorf("error while iterating delegations: %w", err)
	}

	// remove all votes from store
	for _, key := range votesToRemove {
		if err := k.Votes.Remove(ctx, key); err != nil {
			return math.LegacyDec{}, nil, fmt.Errorf("error while removing vote (%d/%s): %w", key.K1(), key.K2(), err)
		}
	}

	return totalVotingPower, results, nil
}

func capVoterPower(votingPower, cap math.LegacyDec) math.LegacyDec {
	if cap.IsZero() || votingPower.LTE(cap) {
		return votingPower
	}

	return cap
}

// getCurrentValidators fetches all the bonded validators, insert them into currValidators
func (k Keeper) getCurrentValidators(ctx context.Context) (map[string]v1.ValidatorGovInfo, error) {
	currValidators := make(map[string]v1.ValidatorGovInfo)
	if err := k.sk.IterateBondedValidatorsByPower(ctx, func(index int64, validator stakingtypes.ValidatorI) (stop bool) {
		valBz, err := k.sk.ValidatorAddressCodec().StringToBytes(validator.GetOperator())
		if err != nil {
			return false
		}
		currValidators[validator.GetOperator()] = v1.NewValidatorGovInfo(
			valBz,
			validator.GetBondedTokens(),
			validator.GetDelegatorShares(),
			math.LegacyZeroDec(),
			v1.WeightedVoteOptions{},
		)

		return false
	}); err != nil {
		return nil, err
	}

	return currValidators, nil
}

// Tally iterates over the votes and updates the tally of a proposal based on the voting power of the
// voters.
//
// DoChain governance customisation:
// - quorum is disabled
// - NoWithVeto cannot veto/fail/burn a proposal
// - abstain is removed from the pass/fail voting total
// - only Yes vs No decides whether a proposal passes
func (k Keeper) Tally(ctx context.Context, proposal v1.Proposal) (passes, burnDeposits bool, tallyResults v1.TallyResult, err error) {
	currValidators, err := k.getCurrentValidators(ctx)
	if err != nil {
		return false, false, tallyResults, fmt.Errorf("error while getting current validators: %w", err)
	}

	tallyFn := k.calculateVoteResultsAndVotingPowerFn
	totalVotingPower, results, err := tallyFn(ctx, k, proposal, currValidators)
	if err != nil {
		return false, false, tallyResults, fmt.Errorf("error while calculating tally results: %w", err)
	}

	tallyResults = v1.NewTallyResultFromMap(results)

	// If there is no staked coins, the proposal fails.
	totalBonded, err := k.sk.TotalBondedTokens(ctx)
	if err != nil {
		return false, false, tallyResults, err
	}

	if totalBonded.IsZero() {
		return false, false, tallyResults, nil
	}

	params, err := k.Params.Get(ctx)
	if err != nil {
		return false, false, tallyResults, fmt.Errorf("error while getting params: %w", err)
	}

	// DoChain: abstain votes are excluded from pass/fail calculation.
	nonAbstainVotingPower := totalVotingPower.Sub(results[v1.OptionAbstain])

	// If no one votes Yes/No, proposal fails.
	if nonAbstainVotingPower.Equal(math.LegacyZeroDec()) {
		return false, false, tallyResults, nil
	}

	var thresholdStr string
	if proposal.Expedited {
		thresholdStr = params.GetExpeditedThreshold()
	} else {
		thresholdStr = params.GetThreshold()
	}

	threshold, _ := math.LegacyNewDecFromStr(thresholdStr)

	// DoChain: proposal passes only if Yes beats the threshold against non-abstain votes.
	if results[v1.OptionYes].Quo(nonAbstainVotingPower).GT(threshold) {
		return true, false, tallyResults, nil
	}

	return false, false, tallyResults, nil
}
