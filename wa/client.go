package wa

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"wami/store"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	_ "modernc.org/sqlite"
)

// Client whatsapp bağlantısını yönetir.
type Client struct {
	wac    *whatsmeow.Client
	db     *store.DB
	qrCode string
	mu     sync.RWMutex
}

func New(db *store.DB) (*Client, error) {
	return NewWithContext(context.Background(), db)
}

func NewWithContext(ctx context.Context, db *store.DB) (*Client, error) {
	dbLog := waLog.Stdout("WADatabase", "ERROR", true)
	container, err := sqlstore.New(ctx, "sqlite", "file:wa.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", dbLog)
	if err != nil {
		return nil, fmt.Errorf("device store: %w", err)
	}

	deviceStore, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, fmt.Errorf("get device: %w", err)
	}

	clientLog := waLog.Stdout("WAClient", "INFO", true)
	wac := whatsmeow.NewClient(deviceStore, clientLog)

	c := &Client{wac: wac, db: db}
	wac.AddEventHandler(c.handleEvent)
	return c, nil
}

// Start WhatsApp bağlantısını başlatır; gerekirse QR akışını yürütür.
func (c *Client) Start(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			c.wac.Disconnect()
			return
		default:
		}

		// Zaten bağlıysa (QR sonrası gibi) sadece ctx'i bekle.
		if c.wac.IsConnected() {
			<-ctx.Done()
			c.wac.Disconnect()
			return
		}

		if c.wac.Store.ID == nil {
			c.loginWithQR(ctx)
		} else {
			if err := c.wac.Connect(); err != nil {
				log.Printf("wa: connect error: %v", err)
				select {
				case <-ctx.Done():
					c.wac.Disconnect()
					return
				case <-time.After(5 * time.Second):
				}
				continue
			}
			<-ctx.Done()
			c.wac.Disconnect()
			return
		}
	}
}

func (c *Client) loginWithQR(ctx context.Context) {
	qrChan, _ := c.wac.GetQRChannel(ctx)
	if err := c.wac.Connect(); err != nil {
		log.Printf("wa: QR connect failed: %v", err)
		return
	}
	for evt := range qrChan {
		switch evt.Event {
		case "code":
			c.mu.Lock()
			c.qrCode = evt.Code
			c.mu.Unlock()
			log.Println("wa: QR kodu güncellendi → http://localhost:8081")
		case "success":
			c.mu.Lock()
			c.qrCode = ""
			c.mu.Unlock()
			log.Println("wa: giriş başarılı!")
			return
		default:
			log.Printf("wa: QR event: %s", evt.Event)
			c.wac.Disconnect()
			return
		}
	}
}

func (c *Client) GetQRCode() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.qrCode
}

func (c *Client) IsConnected() bool { return c.wac.IsConnected() }
func (c *Client) IsLoggedIn() bool  { return c.wac.IsLoggedIn() }

// Logout WhatsApp oturumunu kapatır ve cihaz kaydını siler.
// Bir sonraki Start() çağrısında QR kodu tekrar gösterilir.
func (c *Client) Logout() error {
	return c.wac.Logout(context.Background())
}

// Group tek bir WhatsApp grubunu temsil eder.
type Group struct {
	JID  string
	Name string
}

// GetGroups katıldığı grupların listesini döner.
func (c *Client) GetGroups(ctx context.Context) ([]Group, error) {
	if !c.IsConnected() || !c.IsLoggedIn() {
		return nil, fmt.Errorf("whatsapp bağlı değil")
	}
	raw, err := c.wac.GetJoinedGroups(ctx)
	if err != nil {
		return nil, err
	}
	groups := make([]Group, 0, len(raw))
	for _, g := range raw {
		name := g.Name
		if name == "" {
			name = g.JID.String()
		}
		groups = append(groups, Group{JID: g.JID.String(), Name: name})
	}
	// Ada göre sırala
	for i := 1; i < len(groups); i++ {
		for j := i; j > 0 && groups[j].Name < groups[j-1].Name; j-- {
			groups[j], groups[j-1] = groups[j-1], groups[j]
		}
	}
	return groups, nil
}

// SendMessage verilen numara veya grup JID'ine WhatsApp mesajı gönderir.
// Bireysel: "905551234567"  Grup: "120363XXXXXX@g.us"
func (c *Client) SendMessage(ctx context.Context, phone, message string) error {
	var jid types.JID
	var err error
	if strings.HasSuffix(phone, "@g.us") {
		jid, err = types.ParseJID(phone)
		if err != nil {
			return fmt.Errorf("geçersiz grup JID: %w", err)
		}
	} else {
		jid = types.NewJID(phone, types.DefaultUserServer)
	}
	_, err = c.wac.SendMessage(ctx, jid, &waProto.Message{
		Conversation: proto.String(message),
	})
	if err != nil {
		return err
	}
	_ = c.db.AddHistory(phone, message, "sent")
	return nil
}

// SendImage verilen JID'e resim gönderir (opsiyonel caption ile).
func (c *Client) SendImage(ctx context.Context, phone, filePath, caption, mimeType string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("dosya okunamadı: %w", err)
	}

	var jid types.JID
	if strings.HasSuffix(phone, "@g.us") {
		jid, err = types.ParseJID(phone)
		if err != nil {
			return fmt.Errorf("geçersiz grup JID: %w", err)
		}
	} else {
		jid = types.NewJID(phone, types.DefaultUserServer)
	}

	uploaded, err := c.wac.Upload(ctx, data, whatsmeow.MediaImage)
	if err != nil {
		return fmt.Errorf("upload: %w", err)
	}

	msg := &waProto.Message{
		ImageMessage: &waProto.ImageMessage{
			Mimetype:      proto.String(mimeType),
			Caption:       proto.String(caption),
			URL:           &uploaded.URL,
			DirectPath:    &uploaded.DirectPath,
			MediaKey:      uploaded.MediaKey,
			FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
		},
	}

	_, err = c.wac.SendMessage(ctx, jid, msg)
	if err != nil {
		return err
	}

	desc := "[resim]"
	if caption != "" {
		desc = "[resim] " + caption
	}
	_ = c.db.AddHistory(phone, desc, "sent")
	return nil
}

func (c *Client) handleEvent(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		if v.Info.IsFromMe {
			return
		}
		phone := v.Info.Sender.User
		text := v.Message.GetConversation()
		if text == "" {
			if ext := v.Message.GetExtendedTextMessage(); ext != nil {
				text = ext.GetText()
			}
		}
		if text != "" {
			log.Printf("wa: mesaj alındı %s: %s", phone, text)
			_ = c.db.AddHistory(phone, text, "received")
		}
	}
}
