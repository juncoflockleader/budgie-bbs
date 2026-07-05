package core

import "github.com/juncoflockleader/budgie-bbs/internal/core/projections"

func (c *Core) ListMail(userID, mailbox string, limit, offset int, unreadOnly bool) ([]projections.MailItem, error) {
	return projections.ListMail(c.DB, userID, mailbox, limit, offset, unreadOnly)
}

func (c *Core) ListMailThread(userID, messageID string, limit, offset int) ([]projections.MailItem, error) {
	return projections.ListMailThread(c.DB, userID, messageID, limit, offset)
}

func (c *Core) ListMailByAuthor(userID, messageID string, limit, offset int) ([]projections.MailItem, error) {
	return projections.ListMailByAuthor(c.DB, userID, messageID, limit, offset)
}

func (c *Core) GetMail(userID, messageID string) (*projections.MailItem, error) {
	return projections.GetMail(c.DB, userID, messageID)
}

func (c *Core) CountUnreadMail(userID string) (int, error) {
	return projections.CountUnreadMail(c.DB, userID)
}

func (c *Core) GetMailUsage(userID string) (*projections.MailUsage, error) {
	return projections.GetMailUsage(c.DB, userID)
}

func (c *Core) ListRelayDeliveries(status string, limit, offset int) ([]projections.RelayDelivery, error) {
	return projections.ListRelayDeliveries(c.DB, status, limit, offset)
}

func (c *Core) ListMailGroups(userID string) ([]projections.MailGroup, error) {
	groups, err := projections.ListMailGroups(c.DB, userID)
	if err != nil {
		return nil, err
	}
	friends, err := projections.ListSocialUsers(c.DB, userID, "friends", false)
	if err != nil {
		return nil, err
	}
	friendGroup := projections.MailGroup{ID: "friends", Name: "Friends", BuiltIn: true}
	for i, friend := range friends {
		friendGroup.Members = append(friendGroup.Members, projections.MailGroupMember{
			UserID:   friend.UserID,
			Name:     friend.Name,
			Position: i,
		})
	}
	return append([]projections.MailGroup{friendGroup}, groups...), nil
}

func (c *Core) GetDirectMessageSettings(userID string) (*projections.DirectMessageSettings, error) {
	return projections.GetDirectMessageSettings(c.DB, userID)
}

func (c *Core) ListDirectMessageConversations(userID string, limit, offset int) ([]projections.DirectMessageConversation, error) {
	return projections.ListDirectMessageConversations(c.DB, userID, limit, offset)
}

func (c *Core) ListDirectMessages(userID, otherUserID string, limit, offset int) ([]projections.DirectMessage, error) {
	return projections.ListDirectMessages(c.DB, userID, otherUserID, limit, offset)
}

func (c *Core) CountUnreadDirectMessages(userID string) (int, error) {
	return projections.CountUnreadDirectMessages(c.DB, userID)
}

func (c *Core) ListSocialUsers(userID, list string, onlineOnly bool) ([]projections.SocialUser, error) {
	return projections.ListSocialUsers(c.DB, userID, list, onlineOnly)
}
