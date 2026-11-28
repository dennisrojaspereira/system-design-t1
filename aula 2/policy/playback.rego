# Quem pode dar play? A regra de negócio, escrita como policy (Rego) e avaliada
# pelo OPA. A API monta o `input` com dados de fontes confiáveis — a identidade
# vem do token verificado; matrícula e estado do vídeo vêm do Postgres. Nada
# disso vem do navegador.
#
# Edite este arquivo com o lab rodando: o OPA recarrega sozinho (--watch).
# Experimento: comente a linha da matrícula e dê play como bia.
package vod.playback

import rego.v1

default allow := false

allow if {
	input.action == "play"
	input.entitlement.active == true
	input.resource.status == "READY"
}

# O motivo acompanha a decisão: é o que a API devolve e o que vai para a auditoria.
reason := "sem matrícula neste curso" if {
	not input.entitlement.active
} else := "esta aula ainda não tem vídeo publicado" if {
	input.resource.status != "READY"
} else := "ok"
