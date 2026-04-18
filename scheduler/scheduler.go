package scheduler

import (
	"context"
	"log"
	"strconv"
	"strings"
	"time"

	"wami/store"
	"wami/wa"
)

type Scheduler struct {
	db     *store.DB
	client *wa.Client
}

func New(db *store.DB, client *wa.Client) *Scheduler {
	return &Scheduler{db: db, client: client}
}

// Run her 30 saniyede bir bekleyen mesajları kontrol edip gönderir.
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.process(ctx)
		}
	}
}

func (s *Scheduler) process(ctx context.Context) {
	if !s.client.IsConnected() || !s.client.IsLoggedIn() {
		return
	}

	msgs, err := s.db.GetPendingMessages(time.Now())
	if err != nil {
		log.Printf("scheduler: pending mesajlar alınamadı: %v", err)
		return
	}
	if len(msgs) == 0 {
		return
	}
	log.Printf("scheduler: %d bekleyen mesaj işleniyor", len(msgs))

	for _, msg := range msgs {
		sent, total := s.send(ctx, msg)
		if sent == 0 && total > 0 {
			// Hiçbir JID'e gönderilemedi — bir sonraki tick'te tekrar dene
			log.Printf("scheduler: %s → %d/%d başarısız, tekrar denenecek", msg.Phone, total-sent, total)
			continue
		}
		if sent < total {
			log.Printf("scheduler: %s → %d/%d başarılı (kısmi)", msg.Phone, sent, total)
		}
		// En az 1 JID'e gittiyse: tekrar planla + sent yap
		if msg.RepeatRule != "" {
			nextTime := nextSchedule(msg.ScheduledAt, msg.RepeatRule)
			if !nextTime.IsZero() {
				_ = s.db.CreateScheduledMessage(msg.Phone, msg.Message, nextTime, msg.MsgType, msg.FileID, msg.RepeatRule)
			}
		}
		if err := s.db.MarkAsSent(msg.ID); err != nil {
			log.Printf("scheduler: MarkAsSent %d: %v", msg.ID, err)
		}
	}
}

func nextSchedule(from time.Time, rule string) time.Time {
	switch rule {
	case "daily":
		return from.AddDate(0, 0, 1)
	case "weekly":
		return from.AddDate(0, 0, 7)
	case "monthly":
		return from.AddDate(0, 1, 0)
	}
	return time.Time{}
}

// send tek bir scheduled_message'ı işler.
// phone "cg:ID" formatındaysa contact group üyelerine genişletir.
// send mesajı gönderir ve (gönderilen, toplam) sayısını döner.
func (s *Scheduler) send(ctx context.Context, msg store.ScheduledMessage) (sent int, total int) {
	jids := []string{msg.Phone}

	if strings.HasPrefix(msg.Phone, "cg:") {
		idStr := strings.TrimPrefix(msg.Phone, "cg:")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			log.Printf("scheduler: geçersiz contact group id: %s", idStr)
			return 0, 1
		}
		groupJIDs, err := s.db.GetContactGroupJIDs(id)
		if err != nil {
			log.Printf("scheduler: contact group JID'leri alınamadı: %v", err)
			return 0, 1
		}
		jids = groupJIDs
	}

	total = len(jids)
	for _, jid := range jids {
		sendCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		var err error
		switch msg.MsgType {
		case "image":
			err = s.client.SendImage(sendCtx, jid, msg.FilePath, msg.Message, msg.FileMime)
		case "image_only":
			err = s.client.SendImage(sendCtx, jid, msg.FilePath, "", msg.FileMime)
		default:
			err = s.client.SendMessage(sendCtx, jid, msg.Message)
		}
		cancel()
		if err != nil {
			log.Printf("scheduler: %s → hata: %v", jid, err)
		} else {
			sent++
			log.Printf("scheduler: mesaj gönderildi → %s (%s)", jid, msg.MsgType)
		}
	}
	return sent, total
}
