package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	v1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

func TestPhaseOneValidatorBackingThreshold(t *testing.T) {
	validatorAddrs := simtestutil.CreateIncrementalAccounts(4)
	validators, delegations := newBondedValidatorState(t, validatorAddrs, true)
	govKeeper, _, bankKeeper, stakingKeeper, _, _, ctx := setupGovKeeperWithStakingState(t, validators, delegations)

	proposer := simtestutil.AddTestAddrs(bankKeeper, stakingKeeper, ctx, 1, math.NewInt(30000000))[0]
	proposal, err := govKeeper.SubmitProposal(ctx, TestProposal, "", "phase one", "summary", proposer, false)
	require.NoError(t, err)

	params, err := govKeeper.Params.Get(ctx)
	require.NoError(t, err)

	activated, err := govKeeper.AddDeposit(ctx, proposal.Id, proposer, sdk.NewCoins(params.MinDeposit...))
	require.NoError(t, err)
	require.False(t, activated)

	proposal, err = govKeeper.Proposals.Get(ctx, proposal.Id)
	require.NoError(t, err)
	require.Equal(t, v1.StatusDepositPeriod, proposal.Status)

	require.NoError(t, govKeeper.AddVote(ctx, proposal.Id, validatorAddrs[0], v1.NewNonSplitVoteOption(v1.OptionYes), ""))
	proposal, err = govKeeper.Proposals.Get(ctx, proposal.Id)
	require.NoError(t, err)
	require.Equal(t, v1.StatusDepositPeriod, proposal.Status)

	require.NoError(t, govKeeper.AddVote(ctx, proposal.Id, validatorAddrs[1], v1.NewNonSplitVoteOption(v1.OptionYes), ""))
	proposal, err = govKeeper.Proposals.Get(ctx, proposal.Id)
	require.NoError(t, err)
	require.Equal(t, v1.StatusVotingPeriod, proposal.Status)

	require.ErrorContains(t,
		govKeeper.AddVote(ctx, proposal.Id, validatorAddrs[0], v1.NewNonSplitVoteOption(v1.OptionYes), ""),
		"bonded validators cannot vote during the public voting period",
	)
	require.NoError(t, govKeeper.AddVote(ctx, proposal.Id, proposer, v1.NewNonSplitVoteOption(v1.OptionYes), ""))
}

func TestPhaseOneDoesNotActivateWithoutBondedValidators(t *testing.T) {
	govKeeper, _, bankKeeper, stakingKeeper, _, _, ctx := setupGovKeeperWithStakingState(t, nil, nil)

	proposer := simtestutil.AddTestAddrs(bankKeeper, stakingKeeper, ctx, 1, math.NewInt(30000000))[0]
	proposal, err := govKeeper.SubmitProposal(ctx, TestProposal, "", "phase one", "summary", proposer, false)
	require.NoError(t, err)

	params, err := govKeeper.Params.Get(ctx)
	require.NoError(t, err)

	activated, err := govKeeper.AddDeposit(ctx, proposal.Id, proposer, sdk.NewCoins(params.MinDeposit...))
	require.NoError(t, err)
	require.False(t, activated)

	proposal, err = govKeeper.Proposals.Get(ctx, proposal.Id)
	require.NoError(t, err)
	require.Equal(t, v1.StatusDepositPeriod, proposal.Status)
}

func TestPhaseOneRejectsNonValidatorBacking(t *testing.T) {
	validatorAddrs := simtestutil.CreateIncrementalAccounts(1)
	validators, delegations := newBondedValidatorState(t, validatorAddrs, true)
	govKeeper, _, bankKeeper, stakingKeeper, _, _, ctx := setupGovKeeperWithStakingState(t, validators, delegations)

	proposer := simtestutil.AddTestAddrs(bankKeeper, stakingKeeper, ctx, 1, math.NewInt(30000000))[0]
	proposal, err := govKeeper.SubmitProposal(ctx, TestProposal, "", "phase one", "summary", proposer, false)
	require.NoError(t, err)

	require.ErrorContains(t,
		govKeeper.AddVote(ctx, proposal.Id, proposer, v1.NewNonSplitVoteOption(v1.OptionYes), ""),
		"inactive proposal",
	)
}

func TestPhaseOneRequiresValidatorSelfDelegation(t *testing.T) {
	validatorAddrs := simtestutil.CreateIncrementalAccounts(1)
	validators, delegations := newBondedValidatorState(t, validatorAddrs, false)
	govKeeper, _, bankKeeper, stakingKeeper, _, _, ctx := setupGovKeeperWithStakingState(t, validators, delegations)

	proposer := simtestutil.AddTestAddrs(bankKeeper, stakingKeeper, ctx, 1, math.NewInt(30000000))[0]
	proposal, err := govKeeper.SubmitProposal(ctx, TestProposal, "", "phase one", "summary", proposer, false)
	require.NoError(t, err)

	require.ErrorContains(t,
		govKeeper.AddVote(ctx, proposal.Id, validatorAddrs[0], v1.NewNonSplitVoteOption(v1.OptionYes), ""),
		"phase-one backing requires validator self-delegation",
	)
}

func TestPhaseOneBackingOnlyAcceptsYes(t *testing.T) {
	validatorAddrs := simtestutil.CreateIncrementalAccounts(1)
	validators, delegations := newBondedValidatorState(t, validatorAddrs, true)
	govKeeper, _, bankKeeper, stakingKeeper, _, _, ctx := setupGovKeeperWithStakingState(t, validators, delegations)

	proposer := simtestutil.AddTestAddrs(bankKeeper, stakingKeeper, ctx, 1, math.NewInt(30000000))[0]
	proposal, err := govKeeper.SubmitProposal(ctx, TestProposal, "", "phase one", "summary", proposer, false)
	require.NoError(t, err)

	require.ErrorContains(t,
		govKeeper.AddVote(ctx, proposal.Id, validatorAddrs[0], v1.NewNonSplitVoteOption(v1.OptionNo), ""),
		"phase-one backing only accepts a single YES vote",
	)
}

func newBondedValidatorState(t *testing.T, addrs []sdk.AccAddress, selfDelegated bool) ([]stakingtypes.ValidatorI, map[string][]stakingtypes.DelegationI) {
	t.Helper()

	pubKeys := simtestutil.CreateTestPubKeys(len(addrs))
	validators := make([]stakingtypes.ValidatorI, 0, len(addrs))
	delegations := make(map[string][]stakingtypes.DelegationI, len(addrs))

	for i, addr := range addrs {
		valAddr := sdk.ValAddress(addr)
		validator, err := stakingtypes.NewValidator(valAddr.String(), pubKeys[i], stakingtypes.Description{})
		require.NoError(t, err)

		validator.Status = stakingtypes.Bonded
		validator.Tokens = math.NewInt(100)
		validator.DelegatorShares = math.LegacyNewDec(100)
		validators = append(validators, validator)

		if selfDelegated {
			delegations[addr.String()] = []stakingtypes.DelegationI{
				stakingtypes.NewDelegation(addr.String(), valAddr.String(), math.LegacyNewDec(100)),
			}
		}
	}

	return validators, delegations
}
