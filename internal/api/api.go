package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"

	"github.com/ShivamMiguel/go-chalenge/internal/store/pgstore/pgstore"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

type apiHandler struct {
	q        *pgstore.Queries
	r        *chi.Mux
	upgrader websocket.Upgrader
	subscribers map[string]map[*websocket.Conn]context.CancelFunc
	mu   *sync.Mutex
}

func (h apiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.r.ServeHTTP(w, r)
}

func NewHandler(q *pgstore.Queries) http.Handler {
	a := &apiHandler{
		q:        q,
		upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }},
		subscribers: make(map[string]map[*websocket.Conn]context.CancelFunc),
		mu:   &sync.Mutex{},
		
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.Recoverer, middleware.Logger)

	r.Use(cors.Handler(
		cors.Options{
			AllowedOrigins:   []string{"*"},
			AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS"},
			AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
			ExposedHeaders:   []string{"Link"},
			AllowCredentials: true,
			MaxAge:           300,
		},
	))

	r.Get("/subscribe/{room_id}", a.handleSubscribe)

	r.Route("/api", func(r chi.Router) {
		r.Route("/rooms", func(r chi.Router) {
			r.Post("/", a.handleCreateRoom)
			r.Get("/", a.handleGetRoom)

			r.Route("/{room_id}/messages", func(r chi.Router) {
				r.Post("/", a.handleCreateRoomMessage)
				r.Get("/", a.handleGetRoomMessages)

				r.Route("/{message_id}", func(r chi.Router) {
					r.Get("/", a.handleGetRoomMessage)
					r.Patch("/react", a.handleReactToMessage)
					r.Delete("/react", a.handleRemoveReactFromMessage)
					r.Patch("/answer", a.handleMarketMessageAsAnswered)
				})
			})

		})
	})
	a.r = r
	return a
}
func (h apiHandler) handleCreateRoom(w http.ResponseWriter, r *http.Request)              {
	type _body struct {
		Theme string `json:"theme"`
	}
	var body _body
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
        return
	}
	roomID, err := h.q.InsertRoom(r.Context(), body.Theme)
	println("aqui")
	if err!= nil {
		slog.Error("failed to insert room", "error", err)
        http.Error(w, "something went wrong", http.StatusInternalServerError)
        return
    }
	type response struct {
		ID string `json:"id"`
	}

	data, _:= json.Marshal(response{ID: roomID.String()})
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}
func (h apiHandler) handleGetRoom(w http.ResponseWriter, r *http.Request)                 {}
func (h apiHandler) handleCreateRoomMessage(w http.ResponseWriter, r *http.Request)       {}
func (h apiHandler) handleGetRoomMessages(w http.ResponseWriter, r *http.Request)         {}
func (h apiHandler) handleGetRoomMessage(w http.ResponseWriter, r *http.Request)          {}
func (h apiHandler) handleReactToMessage(w http.ResponseWriter, r *http.Request)          {}
func (h apiHandler) handleRemoveReactFromMessage(w http.ResponseWriter, r *http.Request)  {}
func (h apiHandler) handleMarketMessageAsAnswered(w http.ResponseWriter, r *http.Request) {}

func (h apiHandler) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	rawRoomId := chi.URLParam(r, "room_id")
	roomID, err := uuid.Parse(rawRoomId)

	if err != nil {
		http.Error(w, "Invalid room ID", http.StatusBadRequest)
		return
	}
	_, err = h.q.GetRoom(r.Context(), roomID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "Room not found", http.StatusBadRequest)
			return
		}
		http.Error(w, "something went wrong", http.StatusInternalServerError)
		return
	}
	c, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Warn("failed to upgrade connection", "error", err)
		http.Error(w, "failed to upgrade to ws connection", http.StatusBadRequest)
	}
	defer c.Close()

	ctx, cancel := context.WithCancel(r.Context())

	h.mu.Lock()
		if _, ok := h.subscribers[rawRoomId]; !ok {
			h.subscribers[rawRoomId] = make(map[*websocket.Conn]context.CancelFunc)
}
    slog.Info("new client connected", "room_id", rawRoomId, "client_ip", r.RemoteAddr)
	h.subscribers[rawRoomId][c] = cancel
	h.mu.Unlock()

	<-ctx.Done()
	h.mu.Lock()
	delete(h.subscribers[rawRoomId], c)
	h.mu.Unlock()
}
