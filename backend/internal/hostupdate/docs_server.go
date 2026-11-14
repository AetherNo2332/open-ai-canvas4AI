package hostupdate

import "net/http"

func (s *Server) registerDocs(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/docs/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.manager.DocsSnapshot())
	})
	mux.HandleFunc("POST /v1/docs/check", func(w http.ResponseWriter, r *http.Request) {
		status, err := s.manager.CheckDocs(r.Context())
		docsResponse(w, status, err, http.StatusOK)
	})
	mux.HandleFunc("POST /v1/docs/update", func(w http.ResponseWriter, r *http.Request) {
		var request DocsUpdateRequest
		if err := decodeJSON(r, &request); err != nil {
			writeError(w, http.StatusBadRequest, err, nil)
			return
		}
		status, err := s.manager.StartDocsUpdate(request)
		docsResponse(w, status, err, http.StatusAccepted)
	})
	mux.HandleFunc("POST /v1/docs/rollback", func(w http.ResponseWriter, r *http.Request) {
		var request DocsRollbackRequest
		if err := decodeJSON(r, &request); err != nil {
			writeError(w, http.StatusBadRequest, err, nil)
			return
		}
		status, err := s.manager.StartDocsRollback(request)
		docsResponse(w, status, err, http.StatusAccepted)
	})
}
func docsResponse(w http.ResponseWriter, status DocsStatus, err error, success int) {
	if err != nil {
		writeError(w, http.StatusConflict, err, status)
		return
	}
	writeJSON(w, success, status)
}
