package contract

import (
	"net/http"
	"testing"
)

func TestManagementManifestIncludesArtworkUploadRoute(t *testing.T) {
	manifest := buildManagementManifest()
	for _, route := range manifest.ManagementRoutes {
		if route.Method == http.MethodPost && route.Pattern == "/api/media/artwork-upload/{recordID}" {
			if route.Authorization != "manager-admin-session" ||
				route.Request == nil ||
				route.Request.ContentType != "multipart/form-data" ||
				route.Request.MaxBodyBytes == 0 ||
				route.Success == nil ||
				route.Success.Status != http.StatusCreated ||
				len(route.Request.PathFields) != 1 ||
				len(route.Request.MultipartFields) != 4 ||
				len(route.Errors) == 0 {
				t.Fatalf("artwork-upload route contract is incomplete: %+v", route)
			}
			return
		}
	}
	t.Fatal("management manifest omitted live artwork-upload route")
}
