package server

import "net/http"

func (server *Server) computerUseHTTP(writer http.ResponseWriter, request *http.Request, parts []string) error {
	if server.options.ComputerUse == nil {
		return unsupported("computer-use")
	}
	server.options.ComputerUse.HandleHTTP(writer, request, parts)
	return nil
}
