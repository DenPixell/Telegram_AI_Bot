package domain

type Category struct {
	ID       int64
	FamilyID *int64 // nil - общая категория по умолчанию
	Name     string
}
