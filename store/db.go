package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	db *sql.DB
}

type ScheduledMessage struct {
	ID          int64
	Phone       string
	Message     string
	ScheduledAt time.Time
	Sent        bool
	CreatedAt   time.Time
	MsgType     string // "text", "image", "image_only"
	FileID      int64  // 0 = no media
	RepeatRule  string // "", "daily", "weekly", "monthly"
	FilePath    string // join from media_files
	FileMime    string
}

type MessageHistory struct {
	ID        int64
	Phone     string
	Message   string
	Direction string // "sent" | "received"
	Timestamp time.Time
}

type ContactGroup struct {
	ID        int64
	Name      string
	JIDs      []string // WhatsApp grup JID listesi
	CreatedAt time.Time
}

type MessageTemplate struct {
	ID        int64
	Name      string
	Content   string
	CreatedAt time.Time
}

type MediaFolder struct {
	ID        int64
	ParentID  int64 // 0 = root
	Name      string
	CreatedAt time.Time
}

type MediaFile struct {
	ID        int64
	FolderID  int64 // 0 = root
	Name      string
	Path      string // disk path relative to uploads/
	MimeType  string
	Size      int64
	CreatedAt time.Time
}

func New(path string) (*DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	d := &DB{db: db}
	if err := d.migrate(); err != nil {
		return nil, err
	}
	d.migrateScheduledColumns()
	return d, nil
}

func (d *DB) migrate() error {
	_, err := d.db.Exec(`
		CREATE TABLE IF NOT EXISTS scheduled_messages (
			id           INTEGER  PRIMARY KEY AUTOINCREMENT,
			phone        TEXT     NOT NULL,
			message      TEXT     NOT NULL,
			scheduled_at DATETIME NOT NULL,
			sent         BOOLEAN  NOT NULL DEFAULT FALSE,
			created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS message_history (
			id        INTEGER  PRIMARY KEY AUTOINCREMENT,
			phone     TEXT     NOT NULL,
			message   TEXT     NOT NULL,
			direction TEXT     NOT NULL,
			timestamp DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS contact_groups (
			id         INTEGER  PRIMARY KEY AUTOINCREMENT,
			name       TEXT     NOT NULL,
			jids       TEXT     NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);		CREATE TABLE IF NOT EXISTS message_templates (
			id         INTEGER  PRIMARY KEY AUTOINCREMENT,
			name       TEXT     NOT NULL,
			content    TEXT     NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS media_folders (
			id         INTEGER  PRIMARY KEY AUTOINCREMENT,
			parent_id  INTEGER  NOT NULL DEFAULT 0,
			name       TEXT     NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS media_files (
			id         INTEGER  PRIMARY KEY AUTOINCREMENT,
			folder_id  INTEGER  NOT NULL DEFAULT 0,
			name       TEXT     NOT NULL,
			path       TEXT     NOT NULL,
			mime_type  TEXT     NOT NULL,
			size       INTEGER  NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`)
	return err
}

func (d *DB) Close() error { return d.db.Close() }

func (d *DB) migrateScheduledColumns() {
	// Add columns if they don't exist (ignore errors = already exists)
	d.db.Exec(`ALTER TABLE scheduled_messages ADD COLUMN msg_type TEXT NOT NULL DEFAULT 'text'`)
	d.db.Exec(`ALTER TABLE scheduled_messages ADD COLUMN file_id INTEGER NOT NULL DEFAULT 0`)
	d.db.Exec(`ALTER TABLE scheduled_messages ADD COLUMN repeat_rule TEXT NOT NULL DEFAULT ''`)
}

func (d *DB) CreateScheduledMessage(phone, message string, scheduledAt time.Time, msgType string, fileID int64, repeatRule string) error {
	if msgType == "" {
		msgType = "text"
	}
	_, err := d.db.Exec(
		`INSERT INTO scheduled_messages (phone, message, scheduled_at, msg_type, file_id, repeat_rule) VALUES (?, ?, ?, ?, ?, ?)`,
		phone, message, scheduledAt.UTC().Format(time.RFC3339), msgType, fileID, repeatRule,
	)
	return err
}

func (d *DB) GetPendingMessages(before time.Time) ([]ScheduledMessage, error) {
	rows, err := d.db.Query(
		`SELECT sm.id, sm.phone, sm.message, sm.scheduled_at, sm.msg_type, sm.file_id, sm.repeat_rule,
		        COALESCE(mf.path,''), COALESCE(mf.mime_type,'')
		 FROM scheduled_messages sm
		 LEFT JOIN media_files mf ON mf.id = sm.file_id
		 WHERE sm.sent = FALSE AND sm.scheduled_at <= ? ORDER BY sm.scheduled_at`,
		before.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []ScheduledMessage
	for rows.Next() {
		var m ScheduledMessage
		var scheduledStr string
		if err := rows.Scan(&m.ID, &m.Phone, &m.Message, &scheduledStr, &m.MsgType, &m.FileID, &m.RepeatRule, &m.FilePath, &m.FileMime); err != nil {
			return nil, err
		}
		m.ScheduledAt, _ = time.Parse(time.RFC3339, scheduledStr)
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

func (d *DB) MarkAsSent(id int64) error {
	_, err := d.db.Exec(`UPDATE scheduled_messages SET sent = TRUE WHERE id = ?`, id)
	return err
}

func (d *DB) DeleteScheduledMessage(id int64) error {
	_, err := d.db.Exec(`DELETE FROM scheduled_messages WHERE id = ?`, id)
	return err
}

func (d *DB) GetAllScheduled() ([]ScheduledMessage, error) {
	rows, err := d.db.Query(
		`SELECT sm.id, sm.phone, sm.message, sm.scheduled_at, sm.sent, sm.created_at,
		        sm.msg_type, sm.file_id, sm.repeat_rule,
		        COALESCE(mf.path,''), COALESCE(mf.mime_type,'')
		 FROM scheduled_messages sm
		 LEFT JOIN media_files mf ON mf.id = sm.file_id
		 ORDER BY sm.scheduled_at DESC LIMIT 100`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []ScheduledMessage
	for rows.Next() {
		var m ScheduledMessage
		var scheduledStr, createdStr string
		if err := rows.Scan(&m.ID, &m.Phone, &m.Message, &scheduledStr, &m.Sent, &createdStr, &m.MsgType, &m.FileID, &m.RepeatRule, &m.FilePath, &m.FileMime); err != nil {
			return nil, err
		}
		m.ScheduledAt, _ = time.Parse(time.RFC3339, scheduledStr)
		m.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

func (d *DB) AddHistory(phone, message, direction string) error {
	_, err := d.db.Exec(
		`INSERT INTO message_history (phone, message, direction) VALUES (?, ?, ?)`,
		phone, message, direction,
	)
	return err
}

func (d *DB) GetHistory() ([]MessageHistory, error) {
	rows, err := d.db.Query(
		`SELECT id, phone, message, direction, timestamp
		 FROM message_history ORDER BY timestamp DESC LIMIT 100`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []MessageHistory
	for rows.Next() {
		var h MessageHistory
		if err := rows.Scan(&h.ID, &h.Phone, &h.Message, &h.Direction, &h.Timestamp); err != nil {
			return nil, err
		}
		history = append(history, h)
	}
	return history, rows.Err()
}

func (d *DB) CreateContactGroup(name string, jids []string) (int64, error) {
	res, err := d.db.Exec(
		`INSERT INTO contact_groups (name, jids) VALUES (?, ?)`,
		name, strings.Join(jids, ","),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (d *DB) GetContactGroups() ([]ContactGroup, error) {
	rows, err := d.db.Query(
		`SELECT id, name, jids, created_at FROM contact_groups ORDER BY name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []ContactGroup
	for rows.Next() {
		var g ContactGroup
		var jidsStr string
		if err := rows.Scan(&g.ID, &g.Name, &jidsStr, &g.CreatedAt); err != nil {
			return nil, err
		}
		if jidsStr != "" {
			g.JIDs = strings.Split(jidsStr, ",")
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

func (d *DB) GetContactGroupJIDs(id int64) ([]string, error) {
	var jidsStr string
	err := d.db.QueryRow(`SELECT jids FROM contact_groups WHERE id = ?`, id).Scan(&jidsStr)
	if err != nil {
		return nil, err
	}
	if jidsStr == "" {
		return nil, nil
	}
	return strings.Split(jidsStr, ","), nil
}

func (d *DB) DeleteContactGroup(id int64) error {
	_, err := d.db.Exec(`DELETE FROM contact_groups WHERE id = ?`, id)
	return err
}

func (d *DB) CreateMessageTemplate(name, content string) error {
	_, err := d.db.Exec(
		`INSERT INTO message_templates (name, content) VALUES (?, ?)`,
		name, content,
	)
	return err
}

func (d *DB) GetMessageTemplates() ([]MessageTemplate, error) {
	rows, err := d.db.Query(
		`SELECT id, name, content, created_at FROM message_templates ORDER BY name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []MessageTemplate
	for rows.Next() {
		var t MessageTemplate
		if err := rows.Scan(&t.ID, &t.Name, &t.Content, &t.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}

func (d *DB) DeleteMessageTemplate(id int64) error {
	_, err := d.db.Exec(`DELETE FROM message_templates WHERE id = ?`, id)
	return err
}

// ── Media Folders ──

func (d *DB) CreateMediaFolder(parentID int64, name string) (int64, error) {
	res, err := d.db.Exec(
		`INSERT INTO media_folders (parent_id, name) VALUES (?, ?)`,
		parentID, name,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (d *DB) GetMediaFolders(parentID int64) ([]MediaFolder, error) {
	rows, err := d.db.Query(
		`SELECT id, parent_id, name, created_at FROM media_folders WHERE parent_id = ? ORDER BY name`,
		parentID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []MediaFolder
	for rows.Next() {
		var f MediaFolder
		if err := rows.Scan(&f.ID, &f.ParentID, &f.Name, &f.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, f)
	}
	return list, rows.Err()
}

func (d *DB) GetMediaFolder(id int64) (*MediaFolder, error) {
	var f MediaFolder
	err := d.db.QueryRow(
		`SELECT id, parent_id, name, created_at FROM media_folders WHERE id = ?`, id,
	).Scan(&f.ID, &f.ParentID, &f.Name, &f.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// GetFolderBreadcrumb returns ancestors from root to given folder.
func (d *DB) GetFolderBreadcrumb(id int64) ([]MediaFolder, error) {
	var crumbs []MediaFolder
	cur := id
	for cur > 0 {
		f, err := d.GetMediaFolder(cur)
		if err != nil {
			break
		}
		crumbs = append([]MediaFolder{*f}, crumbs...)
		cur = f.ParentID
	}
	return crumbs, nil
}

func (d *DB) DeleteMediaFolder(id int64) error {
	// Delete child files
	rows, err := d.db.Query(`SELECT id FROM media_files WHERE folder_id = ?`, id)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var fid int64
			rows.Scan(&fid)
			d.DeleteMediaFile(fid)
		}
	}
	// Delete child folders recursively
	rows2, err := d.db.Query(`SELECT id FROM media_folders WHERE parent_id = ?`, id)
	if err == nil {
		defer rows2.Close()
		var childIDs []int64
		for rows2.Next() {
			var cid int64
			rows2.Scan(&cid)
			childIDs = append(childIDs, cid)
		}
		for _, cid := range childIDs {
			d.DeleteMediaFolder(cid)
		}
	}
	_, err = d.db.Exec(`DELETE FROM media_folders WHERE id = ?`, id)
	return err
}

// ── Media Files ──

func (d *DB) CreateMediaFile(folderID int64, name, path, mimeType string, size int64) (int64, error) {
	res, err := d.db.Exec(
		`INSERT INTO media_files (folder_id, name, path, mime_type, size) VALUES (?, ?, ?, ?, ?)`,
		folderID, name, path, mimeType, size,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (d *DB) GetMediaFiles(folderID int64) ([]MediaFile, error) {
	rows, err := d.db.Query(
		`SELECT id, folder_id, name, path, mime_type, size, created_at FROM media_files WHERE folder_id = ? ORDER BY name`,
		folderID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []MediaFile
	for rows.Next() {
		var f MediaFile
		if err := rows.Scan(&f.ID, &f.FolderID, &f.Name, &f.Path, &f.MimeType, &f.Size, &f.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, f)
	}
	return list, rows.Err()
}

func (d *DB) GetMediaFile(id int64) (*MediaFile, error) {
	var f MediaFile
	err := d.db.QueryRow(
		`SELECT id, folder_id, name, path, mime_type, size, created_at FROM media_files WHERE id = ?`, id,
	).Scan(&f.ID, &f.FolderID, &f.Name, &f.Path, &f.MimeType, &f.Size, &f.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (d *DB) DeleteMediaFile(id int64) error {
	_, err := d.db.Exec(`DELETE FROM media_files WHERE id = ?`, id)
	return err
}

func (d *DB) GetAllMediaFolders() ([]MediaFolder, error) {
	rows, err := d.db.Query(`SELECT id, parent_id, name, created_at FROM media_folders ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []MediaFolder
	for rows.Next() {
		var f MediaFolder
		if err := rows.Scan(&f.ID, &f.ParentID, &f.Name, &f.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, f)
	}
	return list, rows.Err()
}

func (d *DB) GetAllMediaFiles() ([]MediaFile, error) {
	rows, err := d.db.Query(`SELECT id, folder_id, name, path, mime_type, size, created_at FROM media_files ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []MediaFile
	for rows.Next() {
		var f MediaFile
		if err := rows.Scan(&f.ID, &f.FolderID, &f.Name, &f.Path, &f.MimeType, &f.Size, &f.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, f)
	}
	return list, rows.Err()
}

// GetScheduledCountByDay verilen yıldaki tüm planlanmış mesajları
// "YYYY-MM-DD" → count şeklinde döner.
func (d *DB) GetScheduledCountByDay(year int) (map[string]int, error) {
	rows, err := d.db.Query(
		`SELECT date(scheduled_at), COUNT(*)
		 FROM scheduled_messages
		 WHERE strftime('%Y', scheduled_at) = ?
		 GROUP BY date(scheduled_at)`,
		fmt.Sprintf("%04d", year),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]int)
	for rows.Next() {
		var day string
		var cnt int
		if err := rows.Scan(&day, &cnt); err != nil {
			return nil, err
		}
		result[day] = cnt
	}
	return result, rows.Err()
}

// ResetAll tüm tabloları temizler (yapıyı korur).
func (d *DB) ResetAll() error {
	_, err := d.db.Exec(`
		DELETE FROM scheduled_messages;
		DELETE FROM message_history;
		DELETE FROM contact_groups;
		DELETE FROM message_templates;
		DELETE FROM media_folders;
		DELETE FROM media_files;
	`)
	return err
}
