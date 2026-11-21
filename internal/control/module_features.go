package control

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/nekopass/nekopass/internal/store"
)

// ModuleFeatures controls whether optional panel modules can be used.
type ModuleFeatures struct {
	Commerce bool `json:"commerce"`
	Invites  bool `json:"invites"`
	Tickets  bool `json:"tickets"`
	Users    bool `json:"users"`
}

func defaultModuleFeatures() ModuleFeatures {
	return ModuleFeatures{Commerce: true, Invites: true, Tickets: true, Users: true}
}

func decodeModuleFeatures(raw []byte) (ModuleFeatures, error) {
	features := defaultModuleFeatures()
	err := json.Unmarshal(raw, &features)
	return features, err
}

const usersDisabled = "普通用户暂不可登录"

type moduleFeaturesKey struct{}

func withIdentity(r *http.Request, user store.User, features ModuleFeatures) *http.Request {
	ctx := context.WithValue(r.Context(), userKey{}, user)
	return r.WithContext(context.WithValue(ctx, moduleFeaturesKey{}, features))
}

func requireModule(enabled func(ModuleFeatures) bool, message string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		features, ok := r.Context().Value(moduleFeaturesKey{}).(ModuleFeatures)
		if !ok {
			fail(w, 503, "暂时无法加载功能开关")
			return
		}
		if !enabled(features) {
			fail(w, 403, message)
			return
		}
		next(w, r)
	}
}
