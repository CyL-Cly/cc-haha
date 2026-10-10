package server

import (
	"errors"
	"net/http"

	"github.com/NanmiCoder/cc-haha/backend/internal/skillregistry"
)

// Skills use the server process's native settings and actual plugin producer.
// Discovery does not allocate a Session or start a model-facing Agent.
func (server *Server) skillsHTTP(writer http.ResponseWriter, request *http.Request, parts []string) error {
	if request.Method != http.MethodGet {
		return methodNotAllowed(request.Method)
	}
	sub := ""
	if len(parts) > 2 {
		sub = parts[2]
	}
	if sub != "" && sub != "detail" && sub != "mentions" {
		return notFound("Unknown skills endpoint: " + sub)
	}
	if server.options.Skills == nil {
		return unsupported("skills")
	}
	query := request.URL.Query()
	var err error
	switch sub {
	case "":
		var skills []skillregistry.Meta
		skills, err = server.options.Skills.List(request.Context(), query.Get("cwd"))
		if err == nil {
			writeJSON(writer, http.StatusOK, struct {
				Skills []skillregistry.Meta `json:"skills"`
			}{skills})
		}
	case "detail":
		var detail *skillregistry.Detail
		detail, err = server.options.Skills.Detail(request.Context(), query.Get("cwd"), query.Get("source"), query.Get("name"))
		if err == nil {
			writeJSON(writer, http.StatusOK, struct {
				Detail *skillregistry.Detail `json:"detail"`
			}{detail})
		}
	case "mentions":
		if server.options.SkillMentions == nil {
			return unsupported("skills/mentions")
		}
		mentions, failure := server.options.SkillMentions(request.Context(), query.Get("cwd"), query.Get("providerId"))
		if failure != nil {
			return failure
		}
		writeJSON(writer, http.StatusOK, mentions)
	}
	var discovery *skillregistry.Error
	if errors.As(err, &discovery) {
		if discovery.Status == http.StatusBadRequest {
			return badRequest(discovery.Message)
		}
		if discovery.Status == http.StatusNotFound {
			return notFound(discovery.Message)
		}
	}
	return err
}
