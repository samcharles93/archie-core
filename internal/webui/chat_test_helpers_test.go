package webui

import "github.com/samcharles93/archie-core/internal/gateway"

func testChatService(router *gateway.Router, sessions gateway.SessionStore, turns *gateway.Turns, models gateway.ModelManager, personas *gateway.PersonaRegistry, updates ChatUpdateService, dangerous *DangerousService) *ChatService {
	return &ChatService{Contract: &gateway.LocalChatAdapter{
		Router: router, Sessions: sessions, Turns: turns, Models: models, Personas: personas,
	}, Updates: updates, Dangerous: dangerous}
}

func testLocalChat(service *ChatService) *gateway.LocalChatAdapter {
	return service.Contract.(*gateway.LocalChatAdapter)
}
