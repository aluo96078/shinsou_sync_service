package httpapi

import (
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/aluo96078/shinsou_sync_service/internal/apierr"
	"github.com/aluo96078/shinsou_sync_service/internal/auth"
	"github.com/aluo96078/shinsou_sync_service/internal/library"
)

// pageParams reads offset and limit with the API defaults (0 and 50, limit at most 100).
func pageParams(r *http.Request) (offset, limit int, err error) {
	q := r.URL.Query()
	offset, limit = 0, 50
	if v := q.Get("offset"); v != "" {
		offset, err = strconv.Atoi(v)
		if err != nil || offset < 0 {
			return 0, 0, apierr.BadRequest("offset must be a non-negative integer")
		}
	}
	if v := q.Get("limit"); v != "" {
		limit, err = strconv.Atoi(v)
		if err != nil || limit < 1 || limit > 100 {
			return 0, 0, apierr.BadRequest("limit must be an integer from 1 to 100")
		}
	}
	return offset, limit, nil
}

func (s *Server) handleLibraryList(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	offset, limit, err := pageParams(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	q := r.URL.Query()
	in := library.Query{Q: q.Get("q"), Category: q.Get("category"), Archived: q.Get("archived"), Sort: q.Get("sort"), Offset: offset, Limit: limit}
	if utf8.RuneCountInString(in.Q) > 200 {
		s.fail(w, r, apierr.BadRequest("q must be at most 200 characters"))
		return
	}
	switch in.Archived {
	case "", "exclude", "include", "only":
	default:
		s.fail(w, r, apierr.BadRequest("archived must be exclude, include or only"))
		return
	}
	switch in.Sort {
	case "", "recent", "title", "added":
	default:
		s.fail(w, r, apierr.BadRequest("sort must be recent, title or added"))
		return
	}
	page, err := s.library.List(r.Context(), p.UserID, in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) handleLibraryGet(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	d, err := s.library.Get(r.Context(), p.UserID, r.PathValue("key"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleCategories(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	cats, err := s.library.Categories(r.Context(), p.UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": cats})
}
