package realtime

import "strconv"

// Topic naming convention. Keep these in one place so the HTTP handlers
// (publisher side) and the WebSocket gateway (subscriber side) can't drift.

func topicChannel(workspaceID, channelID int64) string {
	return "ws:" + strconv.FormatInt(workspaceID, 10) + ":ch:" + strconv.FormatInt(channelID, 10)
}

// TopicWorkspace is reserved for workspace-level events (member add/remove,
// channel create/archive) that fan out to everyone in the workspace. Will be
// used starting M4.
func TopicWorkspace(workspaceID int64) string {
	return "ws:" + strconv.FormatInt(workspaceID, 10)
}
