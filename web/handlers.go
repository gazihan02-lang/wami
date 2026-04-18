package web

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"wami/templates"
)

var istanbulLoc *time.Location

func mustLoadLocation(name string) *time.Location {
	if istanbulLoc != nil {
		return istanbulLoc
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		log.Fatalf("timezone yüklenemedi: %v", err)
	}
	istanbulLoc = loc
	return loc
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	scheduled, err := s.db.GetAllScheduled()
	if err != nil {
		log.Printf("handleIndex: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	connected := s.client.IsConnected() && s.client.IsLoggedIn()
	hasQR := s.client.GetQRCode() != ""

	waGroups, _ := s.client.GetGroups(r.Context())
	contactGroups, _ := s.db.GetContactGroups()

	templates.Index(connected, hasQR, scheduled, waGroups, contactGroups).Render(r.Context(), w)
}

func (s *Server) handleScheduleForm(w http.ResponseWriter, r *http.Request) {
	waGroups, err := s.client.GetGroups(r.Context())
	if err != nil {
		log.Printf("handleScheduleForm: WA grupları alınamadı: %v", err)
		waGroups = nil
	}
	contactGroups, err := s.db.GetContactGroups()
	if err != nil {
		log.Printf("handleScheduleForm: contact gruplar alınamadı: %v", err)
		contactGroups = nil
	}
	msgTpls, err := s.db.GetMessageTemplates()
	if err != nil {
		log.Printf("handleScheduleForm: mesaj şablonları alınamadı: %v", err)
		msgTpls = nil
	}
	mediaFolders, _ := s.db.GetAllMediaFolders()
	mediaFiles, _ := s.db.GetAllMediaFiles()
	templates.ScheduleForm(waGroups, contactGroups, msgTpls, mediaFolders, mediaFiles).Render(r.Context(), w)
}

func (s *Server) handleScheduleSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	phone := r.FormValue("phone")
	message := r.FormValue("message")
	scheduledAtStr := r.FormValue("scheduled_at")
	msgType := r.FormValue("msg_type")
	fileIDStr := r.FormValue("file_id")
	repeatRule := r.FormValue("repeat_rule")

	if msgType == "" {
		msgType = "text"
	}

	// scheduled_date + scheduled_time veya eski scheduled_at formatı
	if scheduledAtStr == "" {
		sd := r.FormValue("scheduled_date")
		st := r.FormValue("scheduled_time")
		if sd != "" && st != "" {
			scheduledAtStr = sd + "T" + st
		}
	}

	if phone == "" || scheduledAtStr == "" {
		http.Error(w, "Hedef ve tarih zorunludur", http.StatusBadRequest)
		return
	}
	// text mesajda mesaj zorunlu, image'da opsiyonel, image_only'de gerekmez
	if msgType == "text" && message == "" {
		http.Error(w, "Mesaj alanı zorunludur", http.StatusBadRequest)
		return
	}

	scheduledAt, err := time.ParseInLocation("2006-01-02T15:04", scheduledAtStr, mustLoadLocation("Europe/Istanbul"))
	if err != nil {
		http.Error(w, "Geçersiz tarih formatı", http.StatusBadRequest)
		return
	}

	var fileID int64
	if fileIDStr != "" {
		fileID, _ = strconv.ParseInt(fileIDStr, 10, 64)
	}
	if (msgType == "image" || msgType == "image_only") && fileID == 0 {
		http.Error(w, "Resim seçilmedi", http.StatusBadRequest)
		return
	}

	if err := s.db.CreateScheduledMessage(phone, message, scheduledAt, msgType, fileID, repeatRule); err != nil {
		log.Printf("handleScheduleSubmit: %v", err)
		http.Error(w, "Kayıt başarısız", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleScheduleDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil || id == 0 {
		http.Error(w, "Geçersiz ID", http.StatusBadRequest)
		return
	}
	if err := s.db.DeleteScheduledMessage(id); err != nil {
		log.Printf("handleScheduleDelete: %v", err)
		http.Error(w, "Silme başarısız", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleArchive(w http.ResponseWriter, r *http.Request) {
	folderID := int64(0)
	if v := r.URL.Query().Get("folder"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			folderID = id
		}
	}
	folders, err := s.db.GetMediaFolders(folderID)
	if err != nil {
		log.Printf("handleArchive folders: %v", err)
	}
	files, err := s.db.GetMediaFiles(folderID)
	if err != nil {
		log.Printf("handleArchive files: %v", err)
	}
	breadcrumb, _ := s.db.GetFolderBreadcrumb(folderID)
	templates.Archive(folderID, breadcrumb, folders, files).Render(r.Context(), w)
}

func (s *Server) handleFolderCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	parentID := int64(0)
	if v := r.FormValue("parent_id"); v != "" {
		parentID, _ = strconv.ParseInt(v, 10, 64)
	}
	name := strings.TrimSpace(r.FormValue("folder_name"))
	if name == "" {
		http.Error(w, "Klasör ismi zorunlu", http.StatusBadRequest)
		return
	}
	if _, err := s.db.CreateMediaFolder(parentID, name); err != nil {
		log.Printf("handleFolderCreate: %v", err)
		http.Error(w, "Oluşturulamadı", http.StatusInternalServerError)
		return
	}
	redirect := "/history"
	if parentID > 0 {
		redirect = fmt.Sprintf("/history?folder=%d", parentID)
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

func (s *Server) handleFolderDelete(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Geçersiz ID", http.StatusBadRequest)
		return
	}
	// Get parent before deleting
	folder, _ := s.db.GetMediaFolder(id)
	parentID := int64(0)
	if folder != nil {
		parentID = folder.ParentID
	}
	// Delete files on disk recursively
	s.deleteMediaFolderFiles(id)
	if err := s.db.DeleteMediaFolder(id); err != nil {
		log.Printf("handleFolderDelete: %v", err)
	}
	redirect := "/history"
	if parentID > 0 {
		redirect = fmt.Sprintf("/history?folder=%d", parentID)
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

func (s *Server) deleteMediaFolderFiles(folderID int64) {
	files, _ := s.db.GetMediaFiles(folderID)
	for _, f := range files {
		os.Remove(f.Path)
	}
	subFolders, _ := s.db.GetMediaFolders(folderID)
	for _, sf := range subFolders {
		s.deleteMediaFolderFiles(sf.ID)
	}
}

func (s *Server) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	const maxUpload = 50 << 20 // 50 MB
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		http.Error(w, "Dosya çok büyük (max 50MB)", http.StatusBadRequest)
		return
	}
	folderID := int64(0)
	if v := r.FormValue("folder_id"); v != "" {
		folderID, _ = strconv.ParseInt(v, 10, 64)
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Dosya gerekli", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Validate mime type
	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = mime.TypeByExtension(filepath.Ext(header.Filename))
	}
	allowed := strings.HasPrefix(mimeType, "image/") ||
		strings.HasPrefix(mimeType, "video/") ||
		strings.HasPrefix(mimeType, "audio/")
	if !allowed {
		http.Error(w, "Sadece resim, video ve ses dosyaları yüklenebilir", http.StatusBadRequest)
		return
	}

	// Ensure uploads dir
	if err := os.MkdirAll("uploads", 0755); err != nil {
		http.Error(w, "Sunucu hatası", http.StatusInternalServerError)
		return
	}

	// Unique filename
	ext := filepath.Ext(header.Filename)
	safeName := fmt.Sprintf("%d%s", time.Now().UnixNano(), ext)
	diskPath := filepath.Join("uploads", safeName)

	dst, err := os.Create(diskPath)
	if err != nil {
		http.Error(w, "Dosya kaydedilemedi", http.StatusInternalServerError)
		return
	}
	defer dst.Close()
	if _, err := io.Copy(dst, file); err != nil {
		http.Error(w, "Dosya yazılamadı", http.StatusInternalServerError)
		return
	}

	if _, err := s.db.CreateMediaFile(folderID, header.Filename, diskPath, mimeType, header.Size); err != nil {
		log.Printf("handleFileUpload db: %v", err)
		os.Remove(diskPath)
		http.Error(w, "Kayıt başarısız", http.StatusInternalServerError)
		return
	}

	redirect := "/history"
	if folderID > 0 {
		redirect = fmt.Sprintf("/history?folder=%d", folderID)
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

func (s *Server) handleFileDelete(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Geçersiz ID", http.StatusBadRequest)
		return
	}
	mf, err := s.db.GetMediaFile(id)
	if err != nil {
		http.Error(w, "Dosya bulunamadı", http.StatusNotFound)
		return
	}
	folderID := mf.FolderID
	os.Remove(mf.Path)
	s.db.DeleteMediaFile(id)

	redirect := "/history"
	if folderID > 0 {
		redirect = fmt.Sprintf("/history?folder=%d", folderID)
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

func (s *Server) handleServeFile(w http.ResponseWriter, r *http.Request) {
	// Serve from uploads/ directory — strip /uploads/ prefix
	p := strings.TrimPrefix(r.URL.Path, "/uploads/")
	p = filepath.Clean(p)
	if strings.Contains(p, "..") {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join("uploads", p))
}

func (s *Server) handleGroupCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("group_name"))
	jids := r.Form["jids"]
	if name == "" || len(jids) == 0 {
		http.Error(w, "İsim ve en az bir grup gerekli", http.StatusBadRequest)
		return
	}
	if _, err := s.db.CreateContactGroup(name, jids); err != nil {
		log.Printf("handleGroupCreate: %v", err)
		http.Error(w, "Kayıt başarısız", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/schedule", http.StatusSeeOther)
}

func (s *Server) handleGroupDelete(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Geçersiz ID", http.StatusBadRequest)
		return
	}
	if err := s.db.DeleteContactGroup(id); err != nil {
		log.Printf("handleGroupDelete: %v", err)
		http.Error(w, "Silme başarısız", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/schedule", http.StatusSeeOther)
}

func (s *Server) handleMsgTplCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("tpl_name"))
	content := strings.TrimSpace(r.FormValue("tpl_content"))
	if name == "" || content == "" {
		http.Error(w, "İsim ve içerik zorunlu", http.StatusBadRequest)
		return
	}
	if err := s.db.CreateMessageTemplate(name, content); err != nil {
		log.Printf("handleMsgTplCreate: %v", err)
		http.Error(w, "Kayıt başarısız", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/schedule", http.StatusSeeOther)
}

func (s *Server) handleMsgTplDelete(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Geçersiz ID", http.StatusBadRequest)
		return
	}
	if err := s.db.DeleteMessageTemplate(id); err != nil {
		log.Printf("handleMsgTplDelete: %v", err)
		http.Error(w, "Silme başarısız", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/schedule", http.StatusSeeOther)
}

func (s *Server) handleSendImage(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	target := strings.TrimSpace(r.FormValue("target"))
	caption := strings.TrimSpace(r.FormValue("caption"))
	fileIDStr := r.FormValue("file_id")
	if target == "" || fileIDStr == "" {
		http.Error(w, "Hedef ve dosya zorunlu", http.StatusBadRequest)
		return
	}
	fileID, err := strconv.ParseInt(fileIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Geçersiz dosya", http.StatusBadRequest)
		return
	}
	mf, err := s.db.GetMediaFile(fileID)
	if err != nil {
		http.Error(w, "Dosya bulunamadı", http.StatusNotFound)
		return
	}

	// target = single JID or cg:ID
	var jids []string
	if strings.HasPrefix(target, "cg:") {
		cgID, _ := strconv.ParseInt(strings.TrimPrefix(target, "cg:"), 10, 64)
		jids, err = s.db.GetContactGroupJIDs(cgID)
		if err != nil || len(jids) == 0 {
			http.Error(w, "Grup bulunamadı", http.StatusBadRequest)
			return
		}
	} else {
		jids = []string{target}
	}

	for _, jid := range jids {
		if err := s.client.SendImage(r.Context(), jid, mf.Path, caption, mf.MimeType); err != nil {
			log.Printf("handleSendImage: %s → %v", jid, err)
		}
	}
	http.Redirect(w, r, "/schedule", http.StatusSeeOther)
}

func (s *Server) handleMediaTree(w http.ResponseWriter, r *http.Request) {
	folders, _ := s.db.GetAllMediaFolders()
	files, _ := s.db.GetAllMediaFiles()

	type fileItem struct {
		ID       int64  `json:"id"`
		FolderID int64  `json:"folder_id"`
		Name     string `json:"name"`
		Path     string `json:"path"`
		MimeType string `json:"mime_type"`
	}
	type folderItem struct {
		ID       int64  `json:"id"`
		ParentID int64  `json:"parent_id"`
		Name     string `json:"name"`
	}

	out := struct {
		Folders []folderItem `json:"folders"`
		Files   []fileItem   `json:"files"`
	}{}
	for _, f := range folders {
		out.Folders = append(out.Folders, folderItem{ID: f.ID, ParentID: f.ParentID, Name: f.Name})
	}
	for _, f := range files {
		out.Files = append(out.Files, fileItem{ID: f.ID, FolderID: f.FolderID, Name: f.Name, Path: f.Path, MimeType: f.MimeType})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (s *Server) handleDestek(w http.ResponseWriter, r *http.Request) {
	templates.Destek().Render(r.Context(), w)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.client.Logout(); err != nil {
		log.Printf("handleLogout: %v", err)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	if err := s.db.ResetAll(); err != nil {
		log.Printf("handleReset: %v", err)
		http.Error(w, "Sıfırlama başarısız", http.StatusInternalServerError)
		return
	}
	// uploads klasörünü de temizle
	os.RemoveAll("uploads")
	os.MkdirAll("uploads", 0o755)
	log.Println("Veritabanı ve dosyalar sıfırlandı")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleQRImage QR kodunu PNG olarak üretip döner.
func (s *Server) handleQRImage(w http.ResponseWriter, r *http.Request) {
	qr := s.client.GetQRCode()
	if qr == "" {
		http.NotFound(w, r)
		return
	}
	png, err := qrcode.Encode(qr, qrcode.Medium, 256)
	if err != nil {
		log.Printf("handleQRImage: %v", err)
		http.Error(w, "QR oluşturulamadı", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	_, _ = w.Write(png)
}

type statusResponse struct {
	Connected bool `json:"connected"`
	LoggedIn  bool `json:"logged_in"`
	HasQR     bool `json:"has_qr"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	resp := statusResponse{
		Connected: s.client.IsConnected(),
		LoggedIn:  s.client.IsLoggedIn(),
		HasQR:     s.client.GetQRCode() != "",
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
