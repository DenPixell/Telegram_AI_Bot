// Генерация уникальных кодов семьи: crypto/rand, алфавит без похожих символов.
package invite

import (
	"crypto/rand"
	"math/big"
)

// Без 0/O, 1/I/L - чтобы код было легко продиктовать.
const alphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// Generate возвращает код длины n. Уникальность гарантирует UNIQUE в БД + повтор при конфликте.
func Generate(n int) (string, error) {
	b := make([]byte, n)
	max := big.NewInt(int64(len(alphabet)))
	for i := range b {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = alphabet[idx.Int64()]
	}
	return string(b), nil
}
