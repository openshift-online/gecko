package storage

import "sigs.k8s.io/controller-runtime/pkg/client"

// EventType represents the type of resource event.
type EventType string

const (
	// EventAdded indicates a new resource was created.
	EventAdded EventType = "ADDED"

	// EventModified indicates an existing resource was updated.
	EventModified EventType = "MODIFIED"

	// EventDeleted indicates a resource was deleted.
	EventDeleted EventType = "DELETED"

	// EventBookmark is a special event used for watch synchronization.
	// It contains no object data, only a resource version marker.
	EventBookmark EventType = "BOOKMARK"
)

// ResourceEvent represents a resource change event with its resource version.
type ResourceEvent struct {
	Type               EventType
	Object             client.Object
	ResourceVersion    string
	ContextFilterValue string // set by stores with context filtering configured

	// PreviousObject holds the state of the resource before the change.
	// It is populated for EventModified events so that consumers can detect
	// field changes (e.g., a RoleBinding subject change) and invalidate
	// both the old and new values. It is nil for EventAdded, EventDeleted,
	// and EventBookmark events.
	PreviousObject client.Object
}
