package domain

// ExpenseScope - чьи траты доступны запросу. Создаётся только конструкторами,
// поэтому нельзя случайно получить «всю семью» нулевым значением.
type ExpenseScope struct {
	familyID int64
	userID   int64 // 0 = вся семья (панель владельца)
}

// UserScope - траты одного пользователя в его семье (бот).
func UserScope(familyID, userID int64) ExpenseScope {
	return ExpenseScope{familyID: familyID, userID: userID}
}

// FamilyScope - все траты семьи (панель владельца).
func FamilyScope(familyID int64) ExpenseScope { return ExpenseScope{familyID: familyID} }

// ScopeOf возвращает область видимости для действия в боте: только свои траты.
func ScopeOf(a Actor) ExpenseScope { return UserScope(a.FamilyID, a.UserID) }

func (s ExpenseScope) FamilyID() int64 { return s.familyID }

// UserID возвращает id пользователя и false, если область - вся семья.
func (s ExpenseScope) UserID() (int64, bool) { return s.userID, s.userID != 0 }

func (s ExpenseScope) Valid() bool { return s.familyID > 0 && s.userID >= 0 }

// ExpenseFilter - фильтры выборки. Нулевые значения означают «без ограничения».
type ExpenseFilter struct {
	Scope      ExpenseScope
	MemberID   int64      // фильтр панели по члену семьи, 0 = все
	From, To   Date       // включительно
	Categories []Category // пусто = все
	// Terms - варианты названия после расширения синонимами («ресторан», «рестик», ...).
	// Совпадение по подстроке в title или comment.
	Terms  []string
	Limit  int // 0 = DefaultLimit, максимум MaxLimit
	Offset int
}

const (
	DefaultLimit = 50
	MaxLimit     = 200
)

// MemberProfile - участник семьи вместе с данными профиля для отображения.
type MemberProfile struct {
	Member
	DisplayName string
	Username    string
}

// ExpenseSummary - итоги по фильтру для сводки панели.
type ExpenseSummary struct {
	Total      Money
	Count      int
	ByCategory []CategoryTotal // по убыванию суммы
	ByMember   []MemberTotal   // по убыванию суммы
	ByDay      []DayTotal      // по возрастанию даты, только дни с тратами
}

type CategoryTotal struct {
	Category Category
	Total    Money
	Count    int
}

type MemberTotal struct {
	UserID int64
	Total  Money
	Count  int
}

type DayTotal struct {
	Date  Date
	Total Money
}
