package timezone

import (
	"fmt"
	"regexp"
	"strconv"
)

var (
	// «utc+3», «gmt -5», «+7», «+5:30», «utc+0530».
	reUTC = regexp.MustCompile(`^(?:utc|gmt)?\s*([+-])\s*(\d{1,2})(?:[:.]?(\d{2}))?$`)
	// «utc», «gmt».
	reUTCZero = regexp.MustCompile(`^(?:utc|gmt)$`)
	// «мск», «мск+2», «msk-1».
	reMSK = regexp.MustCompile(`^(?:мск|msk)(?:\s*([+-])\s*(\d{1,2}))?$`)
)

// russianZones - для целых смещений из диапазона РФ берётся российский пояс (без перехода на
// летнее время), чтобы «UTC+7» сохранялся как Asia/Novosibirsk, а не как абстрактный Etc/GMT-7.
var russianZones = map[int]struct{ zone, label string }{
	2:  {"Europe/Kaliningrad", "Калининград"},
	3:  {"Europe/Moscow", "Москва"},
	4:  {"Europe/Samara", "Самара"},
	5:  {"Asia/Yekaterinburg", "Екатеринбург"},
	6:  {"Asia/Omsk", "Омск"},
	7:  {"Asia/Novosibirsk", "Новосибирск"},
	8:  {"Asia/Irkutsk", "Иркутск"},
	9:  {"Asia/Yakutsk", "Якутск"},
	10: {"Asia/Vladivostok", "Владивосток"},
	11: {"Asia/Magadan", "Магадан"},
	12: {"Asia/Kamchatka", "Камчатка"},
}

// fractionalZones - нецелые смещения (в минутах), для которых нет Etc/GMT-зон.
var fractionalZones = map[int]string{
	-210: "America/St_Johns",
	210:  "Asia/Tehran",
	270:  "Asia/Kabul",
	330:  "Asia/Kolkata",
	345:  "Asia/Kathmandu",
	390:  "Asia/Yangon",
	570:  "Australia/Darwin",
}

// parseOffset распознаёт смещение. ok=false - текст не похож на смещение вообще;
// ok=true с ошибкой - похож, но такого смещения не бывает.
func parseOffset(s string) (z Zone, ok bool, err error) {
	if reUTCZero.MatchString(s) {
		z, err = zoneFor("UTC", "UTC")
		return z, true, err
	}
	if m := reMSK.FindStringSubmatch(s); m != nil {
		hours := 3
		if m[1] != "" {
			d, _ := strconv.Atoi(m[2])
			hours += sign(m[1]) * d
		}
		z, err = offsetZone(hours * 60)
		return z, true, err
	}
	if m := reUTC.FindStringSubmatch(s); m != nil {
		h, _ := strconv.Atoi(m[2])
		mins := 0
		if m[3] != "" {
			mins, _ = strconv.Atoi(m[3])
		}
		if mins >= 60 {
			return Zone{}, true, ErrUnknown
		}
		z, err = offsetZone(sign(m[1]) * (h*60 + mins))
		return z, true, err
	}
	return Zone{}, false, nil
}

func sign(s string) int {
	if s == "-" {
		return -1
	}
	return 1
}

// offsetZone подбирает IANA-зону для смещения в минутах.
func offsetZone(minutes int) (Zone, error) {
	if minutes%60 != 0 {
		name, ok := fractionalZones[minutes]
		if !ok {
			return Zone{}, ErrUnknown
		}
		return zoneFor(name, "")
	}
	h := minutes / 60
	if rz, ok := russianZones[h]; ok {
		return zoneFor(rz.zone, rz.label)
	}
	return etcZone(h)
}

// etcZone - фиксированное смещение. У Etc/GMT знак инвертирован: UTC+5 это Etc/GMT-5.
func etcZone(h int) (Zone, error) {
	if h < -12 || h > 14 {
		return Zone{}, ErrUnknown
	}
	if h == 0 {
		return zoneFor("UTC", "UTC")
	}
	return zoneFor(fmt.Sprintf("Etc/GMT%+d", -h), "")
}
