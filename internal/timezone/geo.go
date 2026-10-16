package timezone

import (
	"fmt"
	"math"
)

// maxCityDistanceKm - дальше этого ближайший город из таблицы не считается ориентиром.
const maxCityDistanceKm = 400

// FromLocation определяет пояс по геолокации Telegram офлайн: пояс ближайшего города из
// встроенной таблицы, а если ближе maxCityDistanceKm городов нет - фиксированное смещение
// по долготе (Etc/GMT). Координаты нигде не сохраняются.
//
// Для РФ и СНГ точности хватает; у границ поясов возможна ошибка, пользователь подтверждает
// результат и может поправить его фразой «мой часовой пояс ...».
func FromLocation(lat, lon float64) (Zone, error) {
	if math.IsNaN(lat) || math.IsNaN(lon) || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return Zone{}, fmt.Errorf("coordinates out of range: %w", ErrUnknown)
	}
	best, bestDist := -1, math.Inf(1)
	for i, c := range cities {
		if d := distanceKm(lat, lon, c.lat, c.lon); d < bestDist {
			best, bestDist = i, d
		}
	}
	if best >= 0 && bestDist <= maxCityDistanceKm {
		c := cities[best]
		return zoneFor(c.zone, displayName(c.names[0]))
	}
	return etcZone(int(math.Round(lon / 15)))
}

// distanceKm - расстояние по большому кругу (формула гаверсинусов).
func distanceKm(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusKm = 6371
	rad := math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLon := (lon2 - lon1) * rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadiusKm * math.Asin(math.Sqrt(a))
}
