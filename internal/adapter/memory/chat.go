package memory

import (
	"context"
	"sync"

	"lanshare/internal/domain"
)

const maxStoredMessages = 200

// ChatRepository implements port.MessageRepository. Messages and the shared
// clipboard are short-lived by design and are not written to disk.
type ChatRepository struct {
	mu       sync.Mutex
	messages map[domain.RoomID][]domain.Message
	clips    map[domain.RoomID]domain.Clip
}

func NewChatRepository() *ChatRepository {
	return &ChatRepository{
		messages: make(map[domain.RoomID][]domain.Message),
		clips:    make(map[domain.RoomID]domain.Clip),
	}
}

func (r *ChatRepository) AppendMessage(_ context.Context, m domain.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := append(r.messages[m.RoomID], m)
	if len(list) > maxStoredMessages {
		list = append([]domain.Message(nil), list[len(list)-maxStoredMessages:]...)
	}
	r.messages[m.RoomID] = list
	return nil
}

func (r *ChatRepository) RecentMessages(_ context.Context, room domain.RoomID, limit int) ([]domain.Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := r.messages[room]
	if limit > 0 && len(list) > limit {
		list = list[len(list)-limit:]
	}
	return append([]domain.Message(nil), list...), nil
}

func (r *ChatRepository) GetClip(_ context.Context, room domain.RoomID) (domain.Clip, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.clips[room], nil
}

func (r *ChatRepository) SetClip(_ context.Context, room domain.RoomID, c domain.Clip) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clips[room] = c
	return nil
}
