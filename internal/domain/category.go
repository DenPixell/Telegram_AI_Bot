package domain

// Category - фиксированный набор, совпадает с CHECK в таблице expenses.
type Category string

const (
	CategoryFood          Category = "еда"
	CategoryTransport     Category = "транспорт"
	CategoryHousing       Category = "жильё"
	CategoryEntertainment Category = "развлечения"
	CategoryHealth        Category = "здоровье"
	CategoryShopping      Category = "покупки"
	CategoryOther         Category = "другое"
)

// Categories в порядке отображения.
var Categories = []Category{
	CategoryFood, CategoryTransport, CategoryHousing, CategoryEntertainment,
	CategoryHealth, CategoryShopping, CategoryOther,
}

func (c Category) Valid() bool {
	for _, v := range Categories {
		if c == v {
			return true
		}
	}
	return false
}

// ParseCategory возвращает категорию или «другое», если значение неизвестно
// (например, модель выдумала свою категорию).
func ParseCategory(s string) Category {
	if c := Category(s); c.Valid() {
		return c
	}
	return CategoryOther
}
