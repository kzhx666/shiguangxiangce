package main

import (
	"net/http"
	"strings"
)

// ownerScope 返回当前登录用户可见的空间集合。
// 家庭共享空间对所有家庭成员可见，个人空间只有本人可见。
func ownerScope(user string) []string {
	if user == "admin" {
		return []string{"lwm", "cr", "family"}
	}
	sp := spaceForUser(user)
	switch sp {
	case "cr":
		return []string{"cr", "family"}
	case "lwm":
		return []string{"lwm", "family"}
	case "family":
		return []string{"family"}
	}
	return []string{"family"}
}

func canSee(user, owner string) bool {
	for _, o := range ownerScope(user) {
		if o == owner {
			return true
		}
	}
	return false
}

func userOf(r *http.Request) string {
	if r == nil {
		return ""
	}
	return r.Header.Get("X-Lumina-User")
}

func ownerIn(owners []string) (string, []any) {
	if len(owners) == 0 {
		return " AND 0", nil
	}
	ph := "?" + strings.Repeat(",?", len(owners)-1)
	args := make([]any, 0, len(owners))
	for _, o := range owners {
		args = append(args, o)
	}
	return " AND owner IN (" + ph + ")", args
}
// visibleOwners 计算本次请求允许的 owner 集合。
// 请求里指定了具体空间时，只有该空间在当前用户的可见范围内才生效。
func (s *Server) visibleOwners(r *http.Request, want string) []string {
	allowed := ownerScope(userOf(r))
	if want == "" || want == "all" {
		return allowed
	}
	for _, o := range allowed {
		if o == want {
			return []string{want}
		}
	}
	return []string{"__none__"}
}