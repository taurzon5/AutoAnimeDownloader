package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"AutoAnimeDownloader/src/internal/daemon"
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/logger"
	"AutoAnimeDownloader/src/internal/nyaa"
)

// Queue is a download queue on the wire (decisions/164). The default queue's priorities are
// config.json's (the same as GET /config's priorities); its animes are empty: it holds every anime
// no other queue lists.
type Queue struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Default    bool            `json:"default"`
	Animes     []int           `json:"animes"`
	Priorities nyaa.Priorities `json:"priorities"`
}

type queueNameRequest struct {
	Name string `json:"name"`
}

type queueOrderRequest struct {
	IDs []string `json:"ids"`
}

type animeQueueRequest struct {
	Queue string `json:"queue"`
}

const maxQueueNameLength = 60

// errQueueRefused carries a 4xx out of a QueueStore.Update.
type errQueueRefused struct {
	status int
	code   string
	msg    string
}

func (e errQueueRefused) Error() string { return e.msg }

func queueStoreOr503(w http.ResponseWriter) *files.QueueStore {
	s := daemon.QueueStore()
	if s == nil {
		JSONError(w, http.StatusServiceUnavailable, "QUEUES_UNAVAILABLE", "Download queues are not available")
	}
	return s
}

// writeQueueError answers a refusal from inside an Update with its status, anything else with 500.
func writeQueueError(w http.ResponseWriter, err error) {
	var refused errQueueRefused
	if errors.As(err, &refused) {
		JSONError(w, refused.status, refused.code, refused.msg)
		return
	}
	logger.Logger.Error().Err(err).Msg("Failed to update the download queues")
	JSONInternalError(w, err)
}

func queuesOut(list []files.DownloadQueue, defaults nyaa.Priorities) []Queue {
	out := make([]Queue, 0, len(list))
	for _, q := range list {
		item := Queue{ID: q.ID, Name: q.Name, Default: q.IsDefault(), Animes: q.Animes, Priorities: defaults}
		if item.Animes == nil {
			item.Animes = []int{}
		}
		if q.Priorities != nil {
			item.Priorities = *q.Priorities
		}
		out = append(out, item)
	}
	return out
}

// queueName checks a queue name against the others (case-insensitive, except the queue itself).
func queueName(raw string, list []files.DownloadQueue, self string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" || len(name) > maxQueueNameLength {
		return "", errQueueRefused{http.StatusBadRequest, "VALIDATION_ERROR", "A queue name has 1 to " + strconv.Itoa(maxQueueNameLength) + " characters"}
	}
	for _, q := range list {
		if q.ID != self && strings.EqualFold(q.Name, name) {
			return "", errQueueRefused{http.StatusConflict, "QUEUE_EXISTS", "A queue is already named " + strconv.Quote(name)}
		}
	}
	return name, nil
}

// handleQueues is GET /queues (the queues in their order) and POST /queues {"name"} (a new queue
// at the end, ranking as a copy of the default queue's).
func handleQueues(server *Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := queueStoreOr503(w)
		if store == nil {
			return
		}
		config, err := server.FileManager.LoadConfigs()
		if err != nil {
			JSONInternalError(w, err)
			return
		}
		switch r.Method {
		case http.MethodGet:
			list, err := store.All()
			if err != nil {
				JSONInternalError(w, err)
				return
			}
			JSONSuccess(w, http.StatusOK, queuesOut(list, config.Priorities))
		case http.MethodPost:
			var req queueNameRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				JSONError(w, http.StatusBadRequest, "INVALID_BODY", `Body must be {"name": "<queue name>"}`)
				return
			}
			var created files.DownloadQueue
			list, err := store.Update(func(list []files.DownloadQueue) ([]files.DownloadQueue, error) {
				name, err := queueName(req.Name, list, "")
				if err != nil {
					return nil, err
				}
				ranking := config.Priorities
				created = files.DownloadQueue{
					ID: files.QueueID(name, func(id string) bool {
						_, taken := files.FindQueue(list, id)
						return taken
					}),
					Name:       name,
					Priorities: &ranking,
				}
				return append(list, created), nil
			})
			if err != nil {
				writeQueueError(w, err)
				return
			}
			q, _ := files.FindQueue(list, created.ID)
			JSONSuccess(w, http.StatusCreated, queuesOut([]files.DownloadQueue{q}, config.Priorities)[0])
		default:
			JSONError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only GET and POST methods are allowed")
		}
	}
}

// handleQueueOrder is PUT /queues/order {"ids": [...]}: every queue's id, in the new order.
func handleQueueOrder(server *Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			JSONError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only PUT method is allowed")
			return
		}
		store := queueStoreOr503(w)
		if store == nil {
			return
		}
		var req queueOrderRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			JSONError(w, http.StatusBadRequest, "INVALID_BODY", `Body must be {"ids": ["<queue id>", ...]}`)
			return
		}
		_, err := store.Update(func(list []files.DownloadQueue) ([]files.DownloadQueue, error) {
			if len(req.IDs) != len(list) {
				return nil, errQueueRefused{http.StatusBadRequest, "VALIDATION_ERROR", "The order must name every queue once"}
			}
			out := make([]files.DownloadQueue, 0, len(list))
			for i, id := range req.IDs {
				q, ok := files.FindQueue(list, id)
				if !ok || slices.Contains(req.IDs[:i], id) {
					return nil, errQueueRefused{http.StatusBadRequest, "VALIDATION_ERROR", "The order must name every queue once"}
				}
				out = append(out, q)
			}
			return out, nil
		})
		if err != nil {
			writeQueueError(w, err)
			return
		}
		JSONSuccess(w, http.StatusOK, map[string]string{"message": "Queue order saved"})
	}
}

// handleQueue is PUT /queues/{id} {"name"} (rename; the id stays) and DELETE /queues/{id} (its
// anime go back to the default queue; the default queue can't be deleted).
func handleQueue(server *Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := queueStoreOr503(w)
		if store == nil {
			return
		}
		id := r.PathValue("id")
		switch r.Method {
		case http.MethodPut:
			var req queueNameRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				JSONError(w, http.StatusBadRequest, "INVALID_BODY", `Body must be {"name": "<queue name>"}`)
				return
			}
			_, err := store.Update(func(list []files.DownloadQueue) ([]files.DownloadQueue, error) {
				i := slices.IndexFunc(list, func(q files.DownloadQueue) bool { return q.ID == id })
				if i < 0 {
					return nil, errQueueRefused{http.StatusNotFound, "QUEUE_NOT_FOUND", "No queue has the id " + strconv.Quote(id)}
				}
				name, err := queueName(req.Name, list, id)
				if err != nil {
					return nil, err
				}
				list[i].Name = name
				return list, nil
			})
			if err != nil {
				writeQueueError(w, err)
				return
			}
			JSONSuccess(w, http.StatusOK, map[string]string{"message": "Queue renamed"})
		case http.MethodDelete:
			if id == files.DefaultQueueID {
				JSONError(w, http.StatusBadRequest, "DEFAULT_QUEUE", "The default queue can't be deleted")
				return
			}
			moved := 0
			_, err := store.Update(func(list []files.DownloadQueue) ([]files.DownloadQueue, error) {
				i := slices.IndexFunc(list, func(q files.DownloadQueue) bool { return q.ID == id })
				if i < 0 {
					return nil, errQueueRefused{http.StatusNotFound, "QUEUE_NOT_FOUND", "No queue has the id " + strconv.Quote(id)}
				}
				moved = len(list[i].Animes)
				return slices.Delete(list, i, i+1), nil
			})
			if err != nil {
				writeQueueError(w, err)
				return
			}
			JSONSuccess(w, http.StatusOK, map[string]int{"moved_to_default": moved})
		default:
			JSONError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only PUT and DELETE methods are allowed")
		}
	}
}

// handleQueuePriorities is PUT /queues/{id}/priorities: the queue's ranking, decoded over the saved
// one so a list the body leaves out keeps its value (as PUT /config/priorities, decisions/161). The
// default queue's ranking is config.json's priorities, saved the same way PUT /config/priorities
// saves them.
func handleQueuePriorities(server *Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			JSONError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only PUT method is allowed")
			return
		}
		store := queueStoreOr503(w)
		if store == nil {
			return
		}
		id := r.PathValue("id")
		body, ok := readPartBody(w, r)
		if !ok {
			return
		}
		if id == files.DefaultQueueID {
			savePart(server, w, "Priorities updated successfully", func(c *files.Config) string {
				merged := c.Priorities
				if err := json.Unmarshal(body, &merged); err != nil {
					return "Invalid priorities: " + err.Error()
				}
				c.Priorities = merged
				return ""
			})
			return
		}
		_, err := store.Update(func(list []files.DownloadQueue) ([]files.DownloadQueue, error) {
			i := slices.IndexFunc(list, func(q files.DownloadQueue) bool { return q.ID == id })
			if i < 0 {
				return nil, errQueueRefused{http.StatusNotFound, "QUEUE_NOT_FOUND", "No queue has the id " + strconv.Quote(id)}
			}
			merged := nyaa.DefaultPriorities()
			if list[i].Priorities != nil {
				merged = *list[i].Priorities
			}
			if err := json.Unmarshal(body, &merged); err != nil {
				return nil, errQueueRefused{http.StatusBadRequest, "VALIDATION_ERROR", "Invalid priorities: " + err.Error()}
			}
			list[i].Priorities = &merged
			return list, nil
		})
		if err != nil {
			writeQueueError(w, err)
			return
		}
		JSONSuccess(w, http.StatusOK, map[string]string{"message": "Priorities updated successfully"})
	}
}

// handleAnimeQueue is PUT /animes/{id}/queue {"queue": "<queue id>"}: moves the anime to that
// queue ("default" or "" for the default queue). It need not be tracked yet: Add anime picks the
// queue before the first download.
func handleAnimeQueue(server *Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			JSONError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only PUT method is allowed")
			return
		}
		mediaID, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || mediaID <= 0 {
			JSONError(w, http.StatusBadRequest, "INVALID_ID", "Invalid anime ID")
			return
		}
		store := queueStoreOr503(w)
		if store == nil {
			return
		}
		var req animeQueueRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			JSONError(w, http.StatusBadRequest, "INVALID_BODY", `Body must be {"queue": "<queue id>"}`)
			return
		}
		if err := moveAnimeToQueue(store, mediaID, req.Queue); err != nil {
			writeQueueError(w, err)
			return
		}
		JSONSuccess(w, http.StatusOK, map[string]string{"message": "Anime moved"})
	}
}

// moveAnimeToQueue puts mediaID in queue id ("" = the default queue), refusing an unknown id.
func moveAnimeToQueue(store *files.QueueStore, mediaID int, id string) error {
	if id == "" {
		id = files.DefaultQueueID
	}
	_, err := store.Update(func(list []files.DownloadQueue) ([]files.DownloadQueue, error) {
		if _, ok := files.FindQueue(list, id); !ok {
			return nil, errQueueRefused{http.StatusBadRequest, "QUEUE_NOT_FOUND", "No queue has the id " + strconv.Quote(id)}
		}
		return files.MoveAnime(list, mediaID, id), nil
	})
	return err
}
