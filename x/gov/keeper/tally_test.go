package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/codec/address"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	v1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

func TestVoteRemovalAfterTally(t *testing.T) {
	govKeeper, authKeeper, bankKeeper, stakingKeeper, _, _, ctx := setupGovKeeper(t)
	authKeeper.EXPECT().AddressCodec().Return(address.NewBech32Codec("cosmos")).AnyTimes()
	stakingKeeper.EXPECT().ValidatorAddressCodec().Return(address.NewBech32Codec("cosmos")).AnyTimes()

	addrs := simtestutil.AddTestAddrsIncremental(bankKeeper, stakingKeeper, ctx, 3, math.NewInt(30000000))

	// Create a test proposal
	tp := TestProposal
	proposal, err := govKeeper.SubmitProposal(ctx, tp, "", "test", "summary", addrs[0], false)
	require.NoError(t, err)
	proposalID := proposal.Id

	// Activate voting period
	proposal.Status = v1.StatusVotingPeriod
	require.NoError(t, govKeeper.SetProposal(ctx, proposal))

	// Add votes from different addresses
	require.NoError(t, govKeeper.AddVote(ctx, proposalID, addrs[0], v1.NewNonSplitVoteOption(v1.OptionYes), ""))
	require.NoError(t, govKeeper.AddVote(ctx, proposalID, addrs[1], v1.NewNonSplitVoteOption(v1.OptionNo), ""))
	require.NoError(t, govKeeper.AddVote(ctx, proposalID, addrs[2], v1.NewNonSplitVoteOption(v1.OptionAbstain), ""))

	// verify votes were added to state
	for i, addr := range addrs {
		vote, err := govKeeper.Votes.Get(ctx, collections.Join(proposalID, addr))
		require.NoError(t, err, "Vote for address %d should exist before tally", i)
		require.NotNil(t, vote, "Vote for address %d should not be nil before tally", i)
	}

	// tally the proposal
	proposal, err = govKeeper.Proposals.Get(ctx, proposalID)
	require.NoError(t, err)
	_, _, _, err = govKeeper.Tally(ctx, proposal)
	require.NoError(t, err)

	// votes should be deleted.
	for i, addr := range addrs {
		_, err := govKeeper.Votes.Get(ctx, collections.Join(proposalID, addr))
		require.Error(t, err, "Vote for address %d should be removed after tally", i)
		require.ErrorIs(t, err, collections.ErrNotFound, "Error should be ErrNotFound for address %d after tally", i)
	}
}

// TestMultipleProposalsVoteRemoval verifies that votes for one proposal are removed
// while votes for another proposal are preserved during tallying
func TestMultipleProposalsVoteRemoval(t *testing.T) {
	govKeeper, authKeeper, bankKeeper, stakingKeeper, _, _, ctx := setupGovKeeper(t)
	authKeeper.EXPECT().AddressCodec().Return(address.NewBech32Codec("cosmos")).AnyTimes()
	stakingKeeper.EXPECT().ValidatorAddressCodec().Return(address.NewBech32Codec("cosmos")).AnyTimes()

	addrs := simtestutil.AddTestAddrsIncremental(bankKeeper, stakingKeeper, ctx, 2, math.NewInt(30000000))

	tp := TestProposal
	proposal1, err := govKeeper.SubmitProposal(ctx, tp, "", "test1", "summary", addrs[0], false)
	require.NoError(t, err)
	proposal1ID := proposal1.Id

	proposal2, err := govKeeper.SubmitProposal(ctx, tp, "", "test2", "summary", addrs[0], false)
	require.NoError(t, err)
	proposal2ID := proposal2.Id

	// activate both proposals
	proposal1.Status = v1.StatusVotingPeriod
	require.NoError(t, govKeeper.SetProposal(ctx, proposal1))
	proposal2.Status = v1.StatusVotingPeriod
	require.NoError(t, govKeeper.SetProposal(ctx, proposal2))

	// add some votes for both proposals
	require.NoError(t, govKeeper.AddVote(ctx, proposal1ID, addrs[0], v1.NewNonSplitVoteOption(v1.OptionYes), ""))
	require.NoError(t, govKeeper.AddVote(ctx, proposal1ID, addrs[1], v1.NewNonSplitVoteOption(v1.OptionNo), ""))

	require.NoError(t, govKeeper.AddVote(ctx, proposal2ID, addrs[0], v1.NewNonSplitVoteOption(v1.OptionNo), ""))
	require.NoError(t, govKeeper.AddVote(ctx, proposal2ID, addrs[1], v1.NewNonSplitVoteOption(v1.OptionYes), ""))

	// votes should eixst
	vote1Addr0, err := govKeeper.Votes.Get(ctx, collections.Join(proposal1ID, addrs[0]))
	require.NoError(t, err)
	require.Equal(t, v1.OptionYes, vote1Addr0.Options[0].Option)
	vote2Addr0, err := govKeeper.Votes.Get(ctx, collections.Join(proposal2ID, addrs[0]))
	require.NoError(t, err)
	require.Equal(t, v1.OptionNo, vote2Addr0.Options[0].Option)

	// only tally proposal1
	proposal1, err = govKeeper.Proposals.Get(ctx, proposal1ID)
	require.NoError(t, err)
	_, _, _, err = govKeeper.Tally(ctx, proposal1)
	require.NoError(t, err)

	// check votes
	for _, addr := range addrs {
		// proposal1 votes should be deleted
		_, err := govKeeper.Votes.Get(ctx, collections.Join(proposal1ID, addr))
		require.Error(t, err)
		require.ErrorIs(t, err, collections.ErrNotFound)

		// proposal2 votes should still exist.
		_, err = govKeeper.Votes.Get(ctx, collections.Join(proposal2ID, addr))
		require.NoError(t, err)
	}
}

func TestCommunityTallyCapsEachWalletAtTwoPointFivePercent(t *testing.T) {
	addrs := simtestutil.CreateIncrementalAccounts(3)
	validatorAcc := addrs[0]
	valAddr := sdk.ValAddress(validatorAcc)
	validator, err := stakingtypes.NewValidator(valAddr.String(), simtestutil.CreateTestPubKeys(1)[0], stakingtypes.Description{})
	require.NoError(t, err)
	validator.Status = stakingtypes.Bonded
	validator.Tokens = math.NewInt(10_000_000)
	validator.DelegatorShares = math.LegacyNewDec(10_000_000)

	voters := addrs[1:]
	delegations := map[string][]stakingtypes.DelegationI{
		voters[0].String(): {
			stakingtypes.NewDelegation(voters[0].String(), valAddr.String(), math.LegacyNewDec(1_000_000)),
		},
		voters[1].String(): {
			stakingtypes.NewDelegation(voters[1].String(), valAddr.String(), math.LegacyNewDec(100_000)),
		},
	}
	govKeeper, _, bankKeeper, stakingKeeper, _, _, ctx := setupGovKeeperWithStakingState(t, []stakingtypes.ValidatorI{validator}, delegations)

	proposer := simtestutil.AddTestAddrs(bankKeeper, stakingKeeper, ctx, 1, math.NewInt(30_000_000))[0]
	proposal, err := govKeeper.SubmitProposal(ctx, TestProposal, "", "cap", "summary", proposer, false)
	require.NoError(t, err)

	proposal.Status = v1.StatusVotingPeriod
	require.NoError(t, govKeeper.SetProposal(ctx, proposal))
	require.NoError(t, govKeeper.AddVote(ctx, proposal.Id, voters[0], v1.NewNonSplitVoteOption(v1.OptionYes), ""))
	require.NoError(t, govKeeper.AddVote(ctx, proposal.Id, voters[1], v1.NewNonSplitVoteOption(v1.OptionNo), ""))

	_, _, tally, err := govKeeper.Tally(ctx, proposal)
	require.NoError(t, err)
	require.Equal(t, "250000", tally.YesCount)
	require.Equal(t, "100000", tally.NoCount)
}

func TestCommunityTallyIgnoresValidatorOperatorVotes(t *testing.T) {
	validatorAcc := simtestutil.CreateIncrementalAccounts(1)[0]
	valAddr := sdk.ValAddress(validatorAcc)
	validator, err := stakingtypes.NewValidator(valAddr.String(), simtestutil.CreateTestPubKeys(1)[0], stakingtypes.Description{})
	require.NoError(t, err)
	validator.Status = stakingtypes.Bonded
	validator.Tokens = math.NewInt(10_000_000)
	validator.DelegatorShares = math.LegacyNewDec(10_000_000)

	delegations := map[string][]stakingtypes.DelegationI{
		validatorAcc.String(): {
			stakingtypes.NewDelegation(validatorAcc.String(), valAddr.String(), math.LegacyNewDec(10_000_000)),
		},
	}
	govKeeper, _, bankKeeper, stakingKeeper, _, _, ctx := setupGovKeeperWithStakingState(t, []stakingtypes.ValidatorI{validator}, delegations)

	proposer := simtestutil.AddTestAddrs(bankKeeper, stakingKeeper, ctx, 1, math.NewInt(30_000_000))[0]
	proposal, err := govKeeper.SubmitProposal(ctx, TestProposal, "", "validator", "summary", proposer, false)
	require.NoError(t, err)

	proposal.Status = v1.StatusVotingPeriod
	require.NoError(t, govKeeper.SetProposal(ctx, proposal))
	require.NoError(t, govKeeper.Votes.Set(ctx, collections.Join(proposal.Id, validatorAcc), v1.NewVote(proposal.Id, validatorAcc, v1.NewNonSplitVoteOption(v1.OptionYes), "")))

	_, _, tally, err := govKeeper.Tally(ctx, proposal)
	require.NoError(t, err)
	require.Equal(t, "0", tally.YesCount)
	require.Equal(t, "0", tally.NoCount)
}
