package keeper_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/codec/address"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	"github.com/cosmos/cosmos-sdk/x/gov/keeper"
	v1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
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
	require.NoError(t, govKeeper.AddVote(ctx, proposalID, addrs[2], v1.NewNonSplitVoteOption(v1.OptionYes), ""))

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

func TestDoChainTallySemantics(t *testing.T) {
	tests := []struct {
		name            string
		totalVoterPower math.LegacyDec
		results         map[v1.VoteOption]math.LegacyDec
		expedited       bool
		expectedPasses  bool
		expectedBurns   bool
		expectedYes     string
		expectedAbstain string
		expectedNo      string
		expectedNoVeto  string
	}{
		{
			name:            "abstain is excluded from pass fail denominator",
			totalVoterPower: math.LegacyNewDec(100),
			results: map[v1.VoteOption]math.LegacyDec{
				v1.OptionYes:        math.LegacyNewDec(40),
				v1.OptionAbstain:    math.LegacyNewDec(50),
				v1.OptionNo:         math.LegacyNewDec(10),
				v1.OptionNoWithVeto: math.LegacyZeroDec(),
			},
			expectedPasses:  true,
			expectedYes:     "40",
			expectedAbstain: "50",
			expectedNo:      "10",
			expectedNoVeto:  "0",
		},
		{
			name:            "no with veto cannot fail or burn a passing proposal",
			totalVoterPower: math.LegacyNewDec(100),
			results: map[v1.VoteOption]math.LegacyDec{
				v1.OptionYes:        math.LegacyNewDec(60),
				v1.OptionAbstain:    math.LegacyZeroDec(),
				v1.OptionNo:         math.LegacyZeroDec(),
				v1.OptionNoWithVeto: math.LegacyNewDec(40),
			},
			expectedPasses:  true,
			expectedBurns:   false,
			expectedYes:     "60",
			expectedAbstain: "0",
			expectedNo:      "0",
			expectedNoVeto:  "40",
		},
		{
			name:            "quorum is disabled",
			totalVoterPower: math.LegacyOneDec(),
			results: map[v1.VoteOption]math.LegacyDec{
				v1.OptionYes:        math.LegacyOneDec(),
				v1.OptionAbstain:    math.LegacyZeroDec(),
				v1.OptionNo:         math.LegacyZeroDec(),
				v1.OptionNoWithVeto: math.LegacyZeroDec(),
			},
			expectedPasses:  true,
			expectedYes:     "1",
			expectedAbstain: "0",
			expectedNo:      "0",
			expectedNoVeto:  "0",
		},
		{
			name:            "yes equal to threshold fails",
			totalVoterPower: math.LegacyNewDec(100),
			results: map[v1.VoteOption]math.LegacyDec{
				v1.OptionYes:        math.LegacyNewDec(50),
				v1.OptionAbstain:    math.LegacyZeroDec(),
				v1.OptionNo:         math.LegacyNewDec(50),
				v1.OptionNoWithVeto: math.LegacyZeroDec(),
			},
			expectedPasses:  false,
			expectedYes:     "50",
			expectedAbstain: "0",
			expectedNo:      "50",
			expectedNoVeto:  "0",
		},
		{
			name:            "all abstain fails",
			totalVoterPower: math.LegacyNewDec(100),
			results: map[v1.VoteOption]math.LegacyDec{
				v1.OptionYes:        math.LegacyZeroDec(),
				v1.OptionAbstain:    math.LegacyNewDec(100),
				v1.OptionNo:         math.LegacyZeroDec(),
				v1.OptionNoWithVeto: math.LegacyZeroDec(),
			},
			expectedPasses:  false,
			expectedYes:     "0",
			expectedAbstain: "100",
			expectedNo:      "0",
			expectedNoVeto:  "0",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tallyFn := func(
				context.Context,
				keeper.Keeper,
				v1.Proposal,
				map[string]v1.ValidatorGovInfo,
			) (math.LegacyDec, map[v1.VoteOption]math.LegacyDec, error) {
				return tc.totalVoterPower, tc.results, nil
			}

			govKeeper, _, _, _, _, _, ctx := setupGovKeeperWithStakingStateAndOptions(
				t,
				nil,
				nil,
				keeper.WithCustomCalculateVoteResultsAndVotingPowerFn(tallyFn),
			)

			passes, burnDeposits, tallyResults, err := govKeeper.Tally(ctx, v1.Proposal{
				Id:        1,
				Expedited: tc.expedited,
			})
			require.NoError(t, err)
			require.Equal(t, tc.expectedPasses, passes)
			require.Equal(t, tc.expectedBurns, burnDeposits)
			require.Equal(t, tc.expectedYes, tallyResults.YesCount)
			require.Equal(t, tc.expectedAbstain, tallyResults.AbstainCount)
			require.Equal(t, tc.expectedNo, tallyResults.NoCount)
			require.Equal(t, tc.expectedNoVeto, tallyResults.NoWithVetoCount)
		})
	}
}
