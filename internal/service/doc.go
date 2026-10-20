// Package service - бизнес-правила и единственная точка мутаций.
// Все методы принимают domain.Actor и проверяют права.
//
// Хранилище сервис видит только через интерфейсы из store.go (объявлены здесь, у потребителя),
// поэтому от PostgreSQL он не зависит: реализацию подставляет cmd/server.
//
// families.go, invites.go, members.go - семьи, приглашения, участники (Families).
// expenses.go - траты (Expenses). auth.go - вход в панель (Auth).
package service
