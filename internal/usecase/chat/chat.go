// Package chat implements short text messages and the shared clipboard.
package chat

import (
	"context"
	"time"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/port"
)

const recentLimit = 100

// MessageView is a message as sent to browsers, in events and responses.
type MessageView struct {
	ID         string    `json:"id"`
	DeviceID   string    `json:"deviceId"`
	DeviceName string    `json:"deviceName"`
	Text       string    `json:"text"`
	At         time.Time `json:"at"`
}

// ClipView is the shared clipboard as sent to browsers.
type ClipView struct {
	Text       string    `json:"text"`
	DeviceID   string    `json:"deviceId"`
	DeviceName string    `json:"deviceName"`
	At         time.Time `json:"at"`
}

func messageView(m domain.Message) MessageView {
	return MessageView{m.ID, string(m.DeviceID), m.DeviceName, m.Text, m.At}
}

func clipView(c domain.Clip) ClipView {
	return ClipView{c.Text, string(c.DeviceID), c.DeviceName, c.At}
}

type Service struct {
	repo   port.MessageRepository
	events port.EventPublisher
	ids    port.IDGenerator
	clock  port.Clock
}

func NewService(repo port.MessageRepository, events port.EventPublisher, ids port.IDGenerator, clock port.Clock) *Service {
	return &Service{repo: repo, events: events, ids: ids, clock: clock}
}

// Send posts a message to the actor's room.
func (s *Service) Send(ctx context.Context, actor domain.Device, text string) (MessageView, error) {
	if !actor.CanChat() {
		return MessageView{}, domain.ErrForbidden
	}
	text, err := domain.CleanText(text, domain.MaxMessageRunes, false)
	if err != nil {
		return MessageView{}, err
	}
	m := domain.Message{
		ID: s.ids.NewID(), RoomID: actor.RoomID, DeviceID: actor.ID,
		DeviceName: actor.Name, Text: text, At: s.clock.Now(),
	}
	if err := s.repo.AppendMessage(ctx, m); err != nil {
		return MessageView{}, err
	}
	v := messageView(m)
	s.events.Publish(port.Event{Type: port.EventMessage, RoomID: actor.RoomID, Data: v})
	return v, nil
}

// Recent returns the latest messages of the actor's room, oldest first.
func (s *Service) Recent(ctx context.Context, actor domain.Device) ([]MessageView, error) {
	if !actor.CanDownload() {
		return nil, domain.ErrForbidden
	}
	list, err := s.repo.RecentMessages(ctx, actor.RoomID, recentLimit)
	if err != nil {
		return nil, err
	}
	out := make([]MessageView, 0, len(list))
	for _, m := range list {
		out = append(out, messageView(m))
	}
	return out, nil
}

// Clipboard returns the room's shared clipboard (empty text if unused).
func (s *Service) Clipboard(ctx context.Context, actor domain.Device) (ClipView, error) {
	if !actor.CanDownload() {
		return ClipView{}, domain.ErrForbidden
	}
	c, err := s.repo.GetClip(ctx, actor.RoomID)
	if err != nil {
		return ClipView{}, err
	}
	return clipView(c), nil
}

// SetClipboard replaces the room's shared clipboard. Empty text clears it.
func (s *Service) SetClipboard(ctx context.Context, actor domain.Device, text string) (ClipView, error) {
	if !actor.CanChat() {
		return ClipView{}, domain.ErrForbidden
	}
	text, err := domain.CleanText(text, domain.MaxClipboardRunes, true)
	if err != nil {
		return ClipView{}, err
	}
	c := domain.Clip{Text: text, DeviceID: actor.ID, DeviceName: actor.Name, At: s.clock.Now()}
	if err := s.repo.SetClip(ctx, actor.RoomID, c); err != nil {
		return ClipView{}, err
	}
	v := clipView(c)
	s.events.Publish(port.Event{Type: port.EventClipboard, RoomID: actor.RoomID, Data: v})
	return v, nil
}
