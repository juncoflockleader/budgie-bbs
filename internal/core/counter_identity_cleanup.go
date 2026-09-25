package core

import (
	"database/sql"

	"github.com/juncoflockleader/budgie-bbs/internal/core/counterstore"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

func (c *Core) snapshotUserCounterIdentityTx(tx *sql.Tx, userID string) (counterstore.UserIdentity, map[string]string, error) {
	if c == nil || c.counterStore == nil {
		return counterstore.UserIdentity{}, nil, nil
	}
	identity := counterstore.UserIdentity{}
	var err error
	if isSQLCounterStore(c.counterStore) {
		identity, err = counterstore.UserCounterIdentity(tx, userID)
	} else {
		identity, err = c.counterStore.UserCounterIdentity(userID)
	}
	if err != nil {
		return counterstore.UserIdentity{}, nil, err
	}
	authors, err := counterstore.ReactionAuthors(tx, identity.Reactions)
	if err != nil {
		return counterstore.UserIdentity{}, nil, err
	}
	return identity, authors, nil
}

func isSQLCounterStore(store counterstore.Store) bool {
	_, ok := store.(sqlCounterStore)
	return ok
}

func cleanupUserCounterIdentityTx(tx *sql.Tx, userID string, identity counterstore.UserIdentity, reactionAuthors map[string]string) error {
	for _, reaction := range identity.Reactions {
		if err := projections.DeleteReaction(tx, reaction.PostID, userID); err != nil {
			return err
		}
		if authorID := reactionAuthors[reaction.PostID]; authorID != "" {
			if err := recordReactionRemovedTx(tx, authorID); err != nil {
				return err
			}
		}
	}
	for _, vote := range identity.PollVotes {
		if err := projections.DeletePollVote(tx, vote.PollID, userID); err != nil {
			return err
		}
	}
	return counterstore.ClearReactionReceived(tx, userID)
}

func (c *Core) cleanupDeletedUserCounterIdentity(userID string, identity counterstore.UserIdentity, reactionAuthors map[string]string) error {
	if c == nil || c.counterStore == nil {
		return nil
	}
	if len(identity.Reactions) == 0 && len(identity.PollVotes) == 0 {
		mutation, err := c.counterStore.BeginMutation()
		if err != nil {
			return err
		}
		defer mutation.Rollback() //nolint
		if err := mutation.ClearReactionReceived(userID); err != nil {
			return err
		}
		return mutation.Commit()
	}
	mutation, err := c.counterStore.BeginMutation()
	if err != nil {
		return err
	}
	defer mutation.Rollback() //nolint
	for _, reaction := range identity.Reactions {
		if err := mutation.DeleteReaction(reaction.PostID, userID); err != nil {
			return err
		}
		if authorID := reactionAuthors[reaction.PostID]; authorID != "" {
			if err := mutation.RecordReactionRemoved(authorID); err != nil {
				return err
			}
		}
	}
	for _, vote := range identity.PollVotes {
		if err := mutation.DeletePollVote(vote.PollID, userID); err != nil {
			return err
		}
	}
	if err := mutation.ClearReactionReceived(userID); err != nil {
		return err
	}
	return mutation.Commit()
}
