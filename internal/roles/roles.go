package roles

var Level = map[string]int{
	"admin":  3,
	"editor": 2,
	"viewer": 1,
	"public": 0,
}

func IsValid(role string) bool {
	_, ok := Level[role]
	return ok
}

func Allowed(userRole, requiredRole string) bool {
	if !IsValid(userRole) || !IsValid(requiredRole) {
		return false
	}
	return Level[userRole] >= Level[requiredRole]
}
