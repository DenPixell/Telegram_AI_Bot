// Package onboarding - конечный автомат регистрации (new -> awaiting_choice ->
// awaiting_family_name -> awaiting_tz -> active). Состояние хранится в users.state.
// Пока пользователь не active, сообщения не уходят в LLM.
//
// TODO(этап 8): fsm.go.
package onboarding
