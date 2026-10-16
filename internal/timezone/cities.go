package timezone

// city - запись встроенной таблицы. names - нормализованные формы (нижний регистр, ё -> е):
// первая используется для отображения, остальные - разговорные варианты и латиница.
// Координаты приблизительные, нужны только для поиска ближайшего города по геолокации.
type city struct {
	names    []string
	zone     string
	lat, lon float64
}

// cities - крупные города РФ и СНГ плюс популярные направления. Расширяется добавлением строк.
var cities = []city{
	// UTC+2
	{[]string{"калининград", "kaliningrad"}, "Europe/Kaliningrad", 54.7, 20.5},

	// UTC+3
	{[]string{"москва", "мск", "moscow"}, "Europe/Moscow", 55.76, 37.62},
	{[]string{"санкт-петербург", "петербург", "питер", "спб", "saint petersburg", "st petersburg"}, "Europe/Moscow", 59.94, 30.31},
	{[]string{"нижний новгород", "нижний", "nizhny novgorod"}, "Europe/Moscow", 56.33, 44.0},
	{[]string{"казань", "kazan"}, "Europe/Moscow", 55.79, 49.12},
	{[]string{"ростов-на-дону", "ростов", "rostov-on-don"}, "Europe/Moscow", 47.23, 39.72},
	{[]string{"краснодар", "krasnodar"}, "Europe/Moscow", 45.04, 38.98},
	{[]string{"сочи", "sochi"}, "Europe/Moscow", 43.6, 39.73},
	{[]string{"воронеж", "voronezh"}, "Europe/Moscow", 51.67, 39.18},
	{[]string{"ярославль", "yaroslavl"}, "Europe/Moscow", 57.63, 39.87},
	{[]string{"тула", "tula"}, "Europe/Moscow", 54.19, 37.62},
	{[]string{"рязань", "ryazan"}, "Europe/Moscow", 54.63, 39.74},
	{[]string{"тверь", "tver"}, "Europe/Moscow", 56.86, 35.9},
	{[]string{"владимир", "vladimir"}, "Europe/Moscow", 56.13, 40.4},
	{[]string{"иваново", "ivanovo"}, "Europe/Moscow", 57.0, 40.97},
	{[]string{"кострома", "kostroma"}, "Europe/Moscow", 57.77, 40.93},
	{[]string{"смоленск", "smolensk"}, "Europe/Moscow", 54.78, 32.05},
	{[]string{"брянск", "bryansk"}, "Europe/Moscow", 53.24, 34.37},
	{[]string{"калуга", "kaluga"}, "Europe/Moscow", 54.51, 36.26},
	{[]string{"орел", "oryol"}, "Europe/Moscow", 52.97, 36.07},
	{[]string{"курск", "kursk"}, "Europe/Moscow", 51.73, 36.19},
	{[]string{"белгород", "belgorod"}, "Europe/Moscow", 50.6, 36.59},
	{[]string{"липецк", "lipetsk"}, "Europe/Moscow", 52.61, 39.59},
	{[]string{"тамбов", "tambov"}, "Europe/Moscow", 52.72, 41.45},
	{[]string{"пенза", "penza"}, "Europe/Moscow", 53.2, 45.0},
	{[]string{"вологда", "vologda"}, "Europe/Moscow", 59.22, 39.89},
	{[]string{"череповец", "cherepovets"}, "Europe/Moscow", 59.13, 37.9},
	{[]string{"мурманск", "murmansk"}, "Europe/Moscow", 68.97, 33.08},
	{[]string{"архангельск", "arkhangelsk"}, "Europe/Moscow", 64.54, 40.54},
	{[]string{"петрозаводск", "petrozavodsk"}, "Europe/Moscow", 61.79, 34.36},
	{[]string{"великий новгород", "новгород", "veliky novgorod"}, "Europe/Moscow", 58.52, 31.27},
	{[]string{"псков", "pskov"}, "Europe/Moscow", 57.82, 28.33},
	{[]string{"сыктывкар", "syktyvkar"}, "Europe/Moscow", 61.67, 50.84},
	{[]string{"чебоксары", "cheboksary"}, "Europe/Moscow", 56.15, 47.25},
	{[]string{"йошкар-ола", "yoshkar-ola"}, "Europe/Moscow", 56.63, 47.89},
	{[]string{"саранск", "saransk"}, "Europe/Moscow", 54.18, 45.18},
	{[]string{"киров", "kirov"}, "Europe/Kirov", 58.6, 49.66},
	{[]string{"волгоград", "volgograd"}, "Europe/Volgograd", 48.71, 44.51},
	{[]string{"ставрополь", "stavropol"}, "Europe/Moscow", 45.04, 41.97},
	{[]string{"махачкала", "makhachkala"}, "Europe/Moscow", 42.98, 47.5},
	{[]string{"грозный", "grozny"}, "Europe/Moscow", 43.32, 45.69},
	{[]string{"владикавказ", "vladikavkaz"}, "Europe/Moscow", 43.02, 44.68},
	{[]string{"нальчик", "nalchik"}, "Europe/Moscow", 43.49, 43.6},
	{[]string{"симферополь", "simferopol"}, "Europe/Simferopol", 44.95, 34.1},
	{[]string{"севастополь", "sevastopol"}, "Europe/Simferopol", 44.6, 33.52},

	// UTC+4
	{[]string{"самара", "samara"}, "Europe/Samara", 53.2, 50.15},
	{[]string{"тольятти", "togliatti"}, "Europe/Samara", 53.51, 49.42},
	{[]string{"ижевск", "izhevsk"}, "Europe/Samara", 56.85, 53.2},
	{[]string{"ульяновск", "ulyanovsk"}, "Europe/Ulyanovsk", 54.32, 48.4},
	{[]string{"саратов", "saratov"}, "Europe/Saratov", 51.53, 46.03},
	{[]string{"астрахань", "astrakhan"}, "Europe/Astrakhan", 46.35, 48.04},

	// UTC+5
	{[]string{"екатеринбург", "екб", "ekaterinburg", "yekaterinburg"}, "Asia/Yekaterinburg", 56.84, 60.6},
	{[]string{"челябинск", "chelyabinsk"}, "Asia/Yekaterinburg", 55.16, 61.4},
	{[]string{"пермь", "perm"}, "Asia/Yekaterinburg", 58.01, 56.25},
	{[]string{"тюмень", "tyumen"}, "Asia/Yekaterinburg", 57.15, 65.53},
	{[]string{"уфа", "ufa"}, "Asia/Yekaterinburg", 54.74, 55.97},
	{[]string{"оренбург", "orenburg"}, "Asia/Yekaterinburg", 51.77, 55.1},
	{[]string{"курган", "kurgan"}, "Asia/Yekaterinburg", 55.44, 65.34},
	{[]string{"магнитогорск", "magnitogorsk"}, "Asia/Yekaterinburg", 53.41, 58.98},
	{[]string{"сургут", "surgut"}, "Asia/Yekaterinburg", 61.25, 73.4},
	{[]string{"ханты-мансийск", "khanty-mansiysk"}, "Asia/Yekaterinburg", 61.0, 69.0},
	{[]string{"салехард", "salekhard"}, "Asia/Yekaterinburg", 66.53, 66.6},

	// UTC+6
	{[]string{"омск", "omsk"}, "Asia/Omsk", 54.99, 73.37},

	// UTC+7
	{[]string{"новосибирск", "новосиб", "nsk", "novosibirsk"}, "Asia/Novosibirsk", 55.03, 82.92},
	{[]string{"барнаул", "barnaul"}, "Asia/Barnaul", 53.35, 83.78},
	{[]string{"томск", "tomsk"}, "Asia/Tomsk", 56.48, 84.95},
	{[]string{"кемерово", "kemerovo"}, "Asia/Novokuznetsk", 55.35, 86.09},
	{[]string{"новокузнецк", "novokuznetsk"}, "Asia/Novokuznetsk", 53.76, 87.12},
	{[]string{"красноярск", "krasnoyarsk"}, "Asia/Krasnoyarsk", 56.01, 92.85},
	{[]string{"абакан", "abakan"}, "Asia/Krasnoyarsk", 53.72, 91.44},
	{[]string{"кызыл", "kyzyl"}, "Asia/Krasnoyarsk", 51.72, 94.45},
	{[]string{"норильск", "norilsk"}, "Asia/Krasnoyarsk", 69.35, 88.2},

	// UTC+8
	{[]string{"иркутск", "irkutsk"}, "Asia/Irkutsk", 52.29, 104.3},
	{[]string{"улан-удэ", "ulan-ude"}, "Asia/Irkutsk", 51.83, 107.58},
	{[]string{"братск", "bratsk"}, "Asia/Irkutsk", 56.15, 101.63},

	// UTC+9
	{[]string{"чита", "chita"}, "Asia/Chita", 52.03, 113.5},
	{[]string{"якутск", "yakutsk"}, "Asia/Yakutsk", 62.03, 129.73},
	{[]string{"благовещенск", "blagoveshchensk"}, "Asia/Yakutsk", 50.27, 127.53},

	// UTC+10
	{[]string{"владивосток", "vladivostok"}, "Asia/Vladivostok", 43.12, 131.89},
	{[]string{"хабаровск", "khabarovsk"}, "Asia/Vladivostok", 48.48, 135.08},
	{[]string{"комсомольск-на-амуре", "komsomolsk-on-amur"}, "Asia/Vladivostok", 50.55, 137.0},

	// UTC+11, +12
	{[]string{"магадан", "magadan"}, "Asia/Magadan", 59.57, 150.8},
	{[]string{"южно-сахалинск", "сахалин", "yuzhno-sakhalinsk"}, "Asia/Sakhalin", 46.96, 142.73},
	{[]string{"петропавловск-камчатский", "камчатка", "petropavlovsk-kamchatsky"}, "Asia/Kamchatka", 53.02, 158.65},
	{[]string{"анадырь", "anadyr"}, "Asia/Anadyr", 64.73, 177.5},

	// СНГ и соседи
	{[]string{"минск", "minsk"}, "Europe/Minsk", 53.9, 27.56},
	{[]string{"киев", "kyiv", "kiev"}, "Europe/Kyiv", 50.45, 30.52},
	{[]string{"кишинев", "chisinau"}, "Europe/Chisinau", 47.01, 28.86},
	{[]string{"алматы", "алма-ата", "almaty"}, "Asia/Almaty", 43.24, 76.89},
	{[]string{"астана", "astana"}, "Asia/Almaty", 51.17, 71.43},
	{[]string{"ташкент", "tashkent"}, "Asia/Tashkent", 41.3, 69.24},
	{[]string{"бишкек", "bishkek"}, "Asia/Bishkek", 42.87, 74.59},
	{[]string{"душанбе", "dushanbe"}, "Asia/Dushanbe", 38.56, 68.79},
	{[]string{"ашхабад", "ashgabat"}, "Asia/Ashgabat", 37.96, 58.33},
	{[]string{"ереван", "yerevan"}, "Asia/Yerevan", 40.18, 44.51},
	{[]string{"тбилиси", "tbilisi"}, "Asia/Tbilisi", 41.72, 44.79},
	{[]string{"баку", "baku"}, "Asia/Baku", 40.41, 49.87},
	{[]string{"рига", "riga"}, "Europe/Riga", 56.95, 24.11},
	{[]string{"вильнюс", "vilnius"}, "Europe/Vilnius", 54.69, 25.28},
	{[]string{"таллин", "tallinn"}, "Europe/Tallinn", 59.44, 24.75},

	// Популярные направления
	{[]string{"стамбул", "istanbul"}, "Europe/Istanbul", 41.01, 28.98},
	{[]string{"анталья", "antalya"}, "Europe/Istanbul", 36.9, 30.7},
	{[]string{"дубай", "dubai"}, "Asia/Dubai", 25.2, 55.27},
	{[]string{"белград", "belgrade"}, "Europe/Belgrade", 44.79, 20.45},
	{[]string{"варшава", "warsaw"}, "Europe/Warsaw", 52.23, 21.01},
	{[]string{"прага", "prague"}, "Europe/Prague", 50.08, 14.44},
	{[]string{"берлин", "berlin"}, "Europe/Berlin", 52.52, 13.4},
	{[]string{"париж", "paris"}, "Europe/Paris", 48.86, 2.35},
	{[]string{"лондон", "london"}, "Europe/London", 51.51, -0.13},
	{[]string{"тель-авив", "tel aviv"}, "Asia/Jerusalem", 32.09, 34.78},
	{[]string{"бангкок", "bangkok"}, "Asia/Bangkok", 13.76, 100.5},
	{[]string{"пхукет", "phuket"}, "Asia/Bangkok", 7.88, 98.39},
	{[]string{"бали", "денпасар", "bali", "denpasar"}, "Asia/Makassar", -8.65, 115.22},
	{[]string{"нью-йорк", "new york"}, "America/New_York", 40.71, -74.0},
}
