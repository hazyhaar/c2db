package cihook55

const (
	PlanPauseBeforeCache = "nommer un Pause avant chaque écriture de cache ou de backing store"
	PlanRevokeOnAbandon  = "incrémenter le jeton que le worker détient avant de jeter l'état"
	PlanRejectVector     = "tout oracle nominal a un vecteur de rejet dans le même paquet"
	PlanStamp            = "RequireStamp sur chaque artefact dit généré"
)

func CaseByCase() []string {
	return []string{
		"identité du jeton (atomic.Uint64, generation, lifetimeToken)",
		"messages Update et helpers de modèle",
		"site AST d'injection overlay (fichier et motif)",
		"seuil de couverture et commande d'émission golden",
	}
}
