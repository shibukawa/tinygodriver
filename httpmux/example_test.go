package httpmux_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/shibukawa/tinygodriver/httpmux"
)

func ExampleServeMux() {
	mux := httpmux.NewServeMux()
	mux.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, r.PathValue("id"))
	})

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("GET", "/users/42", nil))
	fmt.Println(rr.Body.String())
	// Output: 42
}
