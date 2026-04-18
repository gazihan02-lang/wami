package web

import (
	"net/http"

	"wami/store"
	"wami/wa"
)

type Server struct {
	db     *store.DB
	client *wa.Client
	mux    *http.ServeMux
}

func NewServer(db *store.DB, client *wa.Client) *Server {
	s := &Server{
		db:     db,
		client: client,
		mux:    http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	authMiddleware(s.mux).ServeHTTP(w, r)
}

func (s *Server) routes() {
	// Public: login
	s.mux.HandleFunc("GET /login", s.handleLoginPage)
	s.mux.HandleFunc("POST /login", s.handleLoginSubmit)
	s.mux.HandleFunc("POST /auth/logout", s.handleAuthLogout)

	// Protected routes
	s.mux.HandleFunc("GET /", s.handleIndex)
	s.mux.HandleFunc("GET /schedule", s.handleScheduleForm)
	s.mux.HandleFunc("POST /schedule", s.handleScheduleSubmit)
	s.mux.HandleFunc("POST /schedule/delete", s.handleScheduleDelete)
	s.mux.HandleFunc("POST /groups/create", s.handleGroupCreate)
	s.mux.HandleFunc("POST /groups/delete", s.handleGroupDelete)
	s.mux.HandleFunc("POST /msgtpl/create", s.handleMsgTplCreate)
	s.mux.HandleFunc("POST /msgtpl/delete", s.handleMsgTplDelete)
	s.mux.HandleFunc("POST /send/image", s.handleSendImage)
	s.mux.HandleFunc("GET /api/media-tree", s.handleMediaTree)
	s.mux.HandleFunc("GET /history", s.handleArchive)
	s.mux.HandleFunc("POST /archive/folder/create", s.handleFolderCreate)
	s.mux.HandleFunc("POST /archive/folder/rename", s.handleFolderRename)
	s.mux.HandleFunc("POST /archive/folder/delete", s.handleFolderDelete)
	s.mux.HandleFunc("POST /archive/upload", s.handleFileUpload)
	s.mux.HandleFunc("POST /archive/file/delete", s.handleFileDelete)
	s.mux.HandleFunc("GET /uploads/", s.handleServeFile)
	s.mux.HandleFunc("GET /destek", s.handleDestek)
	s.mux.HandleFunc("POST /logout", s.handleLogout)
	s.mux.HandleFunc("POST /reset", s.handleReset)
	s.mux.HandleFunc("GET /api/qr.png", s.handleQRImage)
	s.mux.HandleFunc("GET /api/status", s.handleStatus)
}
