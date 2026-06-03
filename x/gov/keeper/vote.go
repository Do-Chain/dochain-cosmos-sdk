package keeper

import (
	"context"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/gov/types"
	v1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
)

// AddVote adds a vote on a specific proposal
func (k Keeper) AddVote(ctx context.Context, proposalID uint64, voterAddr sdk.AccAddress, options v1.WeightedVoteOptions, metadata string) error {
	err := k.assertMetadataLength(metadata)
	if err != nil {
		return err
	}

	for _, option := range options {
		if option == nil {
			return errors.Wrap(types.ErrInvalidVote, "<nil>")
		}
		if !v1.ValidWeightedVoteOption(*option) {
			return errors.Wrap(types.ErrInvalidVote, option.String())
		}
	}

	// Check if proposal is in voting period.
	inVotingPeriod, err := k.VotingPeriodProposals.Has(ctx, proposalID)
	if err != nil {
		return err
	}

	if !inVotingPeriod {
		proposal, err := k.Proposals.Get(ctx, proposalID)
		if err != nil {
			if errors.IsOf(err, collections.ErrNotFound) {
				return errors.Wrapf(types.ErrInactiveProposal, "%d", proposalID)
			}
			return err
		}

		if proposal.Status == v1.StatusDepositPeriod {
			isValidator, err := k.isBondedValidator(ctx, voterAddr)
			if err != nil {
				return err
			}
			if !isValidator {
				return errors.Wrapf(types.ErrInactiveProposal, "%d", proposalID)
			}

			_, err = k.AddProposalBacking(ctx, proposalID, voterAddr, options, metadata)
			return err
		}

		return errors.Wrapf(types.ErrInactiveProposal, "%d", proposalID)
	}

	isValidator, err := k.isBondedValidator(ctx, voterAddr)
	if err != nil {
		return err
	}
	if isValidator {
		return errors.Wrap(types.ErrInvalidVote, "bonded validators cannot vote during the public voting period")
	}

	vote := v1.NewVote(proposalID, voterAddr, options, metadata)
	err = k.Votes.Set(ctx, collections.Join(proposalID, voterAddr), vote)
	if err != nil {
		return err
	}

	// called after a vote on a proposal is cast
	err = k.Hooks().AfterProposalVote(ctx, proposalID, voterAddr)
	if err != nil {
		return err
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	sdkCtx.EventManager().EmitEvent(
		sdk.NewEvent(
			types.EventTypeProposalVote,
			sdk.NewAttribute(types.AttributeKeyVoter, voterAddr.String()),
			sdk.NewAttribute(types.AttributeKeyOption, options.String()),
			sdk.NewAttribute(types.AttributeKeyProposalID, fmt.Sprintf("%d", proposalID)),
		),
	)

	return nil
}

// deleteVotes deletes all the votes from a given proposalID.
func (k Keeper) deleteVotes(ctx context.Context, proposalID uint64) error {
	rng := collections.NewPrefixedPairRange[uint64, sdk.AccAddress](proposalID)
	err := k.Votes.Clear(ctx, rng)
	if err != nil {
		return err
	}

	return nil
}
