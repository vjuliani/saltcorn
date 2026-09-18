package realtime

import "errors"

// ErrInvalidAudience é retornado por Publish quando audience não é um dos
// valores válidos (AudienceBroadcast, AudienceUsers).
var ErrInvalidAudience = errors.New("realtime: audience inválida")

// ErrInvalidUserIDs é retornado por Publish quando userIDs não combina com
// audience: AudienceUsers exige ao menos um id; AudienceBroadcast exige
// nenhum — a mesma invariante que o CHECK de schema.go aplica no banco,
// verificada aqui antes para dar um erro Go claro em vez de um erro de
// constraint do Postgres.
var ErrInvalidUserIDs = errors.New("realtime: user_ids não combina com a audience informada")
