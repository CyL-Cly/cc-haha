package tui

// TeammateMessageOrigin is transient presentation metadata. The mailbox
// attachment and user-XML components deliberately render some notices
// differently; their shared JSON message body cannot identify the caller.
type TeammateMessageOrigin string

const TeammateMessageAttachment TeammateMessageOrigin = "teammate_mailbox"
