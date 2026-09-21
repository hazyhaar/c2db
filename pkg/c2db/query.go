// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"io"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// Entry représente une paire clé-valeur renvoyée par le moteur de requêtes c2db.
type Entry struct {
	Key []byte
	Val []byte
}

// Predicate est un prédicat de filtrage complet opérant sur la clé et la valeur décodée.
type Predicate func(key, val []byte) bool

// KeyPredicate est un prédicat ultra-rapide opérant exclusivement sur la clé (pushdown immédiat).
type KeyPredicate func(key []byte) bool

// RawValPredicate est un prédicat opérant directement sur la cellule brute de feuille B-Tree
// sans matérialiser ni déchiffrer les pages d'overflow.
type RawValPredicate func(rawVal []byte) bool

// Query fournit un constructeur déclaratif fluide et typé de requêtes pour agents autonomes (Agent-First).
// Conçu pour éliminer le surcoût de parsing SQL, les hallucinations de requêtes des LLMs
// et maximiser les performances par refoulement de prédicats (Predicate Pushdown).
type Query struct {
	cursor   *Cursor
	ownsCur  bool
	prefix   []byte
	start    []byte
	end      []byte
	reverse  bool
	offset   int
	limit    int
	keyPreds []KeyPredicate
	rawPreds []RawValPredicate
	allPreds []Predicate
	mu       sync.Mutex
}

// newQuery instancie une nouvelle Query sur un Cursor.
func newQuery(c *Cursor, ownsCur bool) *Query {
	return &Query{
		cursor:  c,
		ownsCur: ownsCur,
		limit:   -1,
	}
}

// Query ouvre un constructeur de requête déclarative Agent-First sur le Shard.
// Le Cursor MVCC sous-jacent est automatiquement fermé lors de l'exécution terminale.
func (s *Shard) Query() (*Query, error) {
	c, err := s.Cursor()
	if err != nil {
		return nil, err
	}
	return newQuery(c, true), nil
}

// Query ouvre un constructeur de requête déclarative Agent-First sur la View isolée.
// Le Cursor MVCC sous-jacent est automatiquement fermé lors de l'exécution terminale.
func (v *View) Query() (*Query, error) {
	c, err := v.Cursor()
	if err != nil {
		return nil, err
	}
	return newQuery(c, true), nil
}

// Query initialise une requête sur un Cursor existant sans s'approprier sa clôture.
func (c *Cursor) Query() *Query {
	return newQuery(c, false)
}

// Prefix restreint la requête aux clés débutant par le préfixe spécifié.
// Calcule automatiquement la borne supérieure lexicographique pour un arrêt anticipé O(log N).
func (q *Query) Prefix(prefix []byte) *Query {
	q.prefix = prefix
	q.start = prefix
	q.end = prefixUpperBound(prefix)
	return q
}

// Range définit l'intervalle semi-ouvert de clés [start, end[.
// Si start est vide ou nil, commence à la première clé. Si end est nil, va jusqu'à la fin.
func (q *Query) Range(start, end []byte) *Query {
	q.start = start
	q.end = end
	return q
}

// Where ajoute un ou plusieurs prédicats combinés par conjonction logique (ET).
func (q *Query) Where(preds ...Predicate) *Query {
	q.allPreds = append(q.allPreds, preds...)
	return q
}

// WhereKey ajoute un prédicat ultra-rapide pushdown exécuté uniquement sur la clé.
// Si la clé est rejetée, la valeur n'est jamais résolue ni allouée.
func (q *Query) WhereKey(preds ...KeyPredicate) *Query {
	q.keyPreds = append(q.keyPreds, preds...)
	return q
}

// WhereRawVal ajoute un prédicat pushdown exécuté directement sur la tranche brute de cellule
// avant tout décodage ou lecture de pages d'overflow.
func (q *Query) WhereRawVal(preds ...RawValPredicate) *Query {
	q.rawPreds = append(q.rawPreds, preds...)
	return q
}

// Limit borne le nombre maximal de résultats retournés.
func (q *Query) Limit(limit int) *Query {
	q.limit = limit
	return q
}

// Offset saute les n premiers résultats satisfaisant l'ensemble des prédicats.
func (q *Query) Offset(offset int) *Query {
	q.offset = offset
	return q
}

// Reverse inverse le sens de parcours (parcours descendant de la plus grande à la plus petite clé).
func (q *Query) Reverse() *Query {
	q.reverse = true
	return q
}

// --- Prédicats d'Usine Agent-First ---

// KeyPrefix filtre les clés commençant par le préfixe spécifié.
func KeyPrefix(prefix []byte) KeyPredicate {
	return func(k []byte) bool {
		return bytes.HasPrefix(k, prefix)
	}
}

// KeySuffix filtre les clés se terminant par le suffixe spécifié.
func KeySuffix(suffix []byte) KeyPredicate {
	return func(k []byte) bool {
		return bytes.HasSuffix(k, suffix)
	}
}

// KeyContains filtre les clés contenant la sous-chaîne spécifiée.
func KeyContains(sub []byte) KeyPredicate {
	return func(k []byte) bool {
		return bytes.Contains(k, sub)
	}
}

// KeyBetween filtre les clés comprises dans l'intervalle [start, end[.
func KeyBetween(start, end []byte) KeyPredicate {
	return func(k []byte) bool {
		if len(start) > 0 && bytes.Compare(k, start) < 0 {
			return false
		}
		if len(end) > 0 && bytes.Compare(k, end) >= 0 {
			return false
		}
		return true
	}
}

// ValContains filtre les valeurs contenant le sous-motif spécifié (recherche byte-search accélérée).
func ValContains(sub []byte) Predicate {
	return func(_, v []byte) bool {
		return bytes.Contains(v, sub)
	}
}

// ValHasPrefix filtre les valeurs débutant par le motif d'octets spécifié (utile pour en-têtes binaires/magic).
func ValHasPrefix(prefix []byte) Predicate {
	return func(_, v []byte) bool {
		return bytes.HasPrefix(v, prefix)
	}
}

// ValHasSuffix filtre les valeurs se terminant par le motif d'octets spécifié.
func ValHasSuffix(suffix []byte) Predicate {
	return func(_, v []byte) bool {
		return bytes.HasSuffix(v, suffix)
	}
}

// ValMinLength filtre les valeurs dont la taille est supérieure ou égale à minLen.
// Pushdown : évalue directement la taille dans le descripteur d'overflow sans déchiffrer le corps.
func ValMinLength(minLen int) RawValPredicate {
	return func(rawVal []byte) bool {
		if tLen, _, ok := isOverflowDescriptor(rawVal); ok {
			return int(tLen) >= minLen
		}
		l := len(rawVal)
		if l > 0 && rawVal[0] == cellKindInline {
			l--
		}
		return l >= minLen
	}
}

// ValMaxLength filtre les valeurs dont la taille est inférieure ou égale à maxLen.
// Pushdown : évalue directement la taille dans le descripteur d'overflow sans déchiffrer le corps.
func ValMaxLength(maxLen int) RawValPredicate {
	return func(rawVal []byte) bool {
		if tLen, _, ok := isOverflowDescriptor(rawVal); ok {
			return int(tLen) <= maxLen
		}
		l := len(rawVal)
		if l > 0 && rawVal[0] == cellKindInline {
			l--
		}
		return l <= maxLen
	}
}

// ValJSONFieldString extrait et compare une chaîne JSON "field":"expected"
// par scan direct sans allocation d'arbre AST ou de désérialisation complète.
func ValJSONFieldString(field, expected string) Predicate {
	return func(_, v []byte) bool {
		val, found := fastExtractJSONField(v, field)
		if !found {
			return false
		}
		return val == expected
	}
}

// ValJSONFieldContains extrait un champ JSON chaîne et vérifie s'il contient une sous-chaîne.
func ValJSONFieldContains(field, sub string) Predicate {
	return func(_, v []byte) bool {
		val, found := fastExtractJSONField(v, field)
		if !found {
			return false
		}
		return strings.Contains(val, sub)
	}
}

// --- Exécutions Terminales ---

// Count compte le nombre d'entrées satisfaisant l'ensemble des prédicats
// avec pushdown intégral : ne matérialise jamais les corps de valeurs en mémoire.
func (q *Query) Count() (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.cursor == nil {
		return 0, unix.EBADF
	}
	if q.ownsCur {
		defer q.cursor.Close()
	}

	count := 0
	err := q.iterate(false, false, func(k, _ []byte, _ io.Reader) bool {
		count++
		return true
	})
	return count, err
}

// First retourne la première paire clé-valeur satisfaisant la requête.
// Si aucune entrée ne correspond, renvoie ErrNotFound.
func (q *Query) First() (Entry, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.cursor == nil {
		return Entry{}, unix.EBADF
	}
	if q.ownsCur {
		defer q.cursor.Close()
	}

	var found Entry
	var ok bool

	err := q.iterate(true, false, func(k, v []byte, _ io.Reader) bool {
		found = Entry{
			Key: append([]byte(nil), k...),
			Val: append([]byte(nil), v...),
		}
		ok = true
		return false // Arrêt immédiat dès la première correspondance
	})
	if err != nil {
		return Entry{}, err
	}
	if !ok {
		return Entry{}, ErrNotFound
	}
	return found, nil
}

// Collect matérialise et retourne l'ensemble des paires clé-valeur correspondantes.
func (q *Query) Collect() ([]Entry, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.cursor == nil {
		return nil, unix.EBADF
	}
	if q.ownsCur {
		defer q.cursor.Close()
	}

	capEstimate := 16
	if q.limit > 0 && q.limit < capEstimate {
		capEstimate = q.limit
	}
	results := make([]Entry, 0, capEstimate)

	err := q.iterate(true, false, func(k, v []byte, _ io.Reader) bool {
		results = append(results, Entry{
			Key: append([]byte(nil), k...),
			Val: append([]byte(nil), v...),
		})
		return true
	})
	return results, err
}

// CollectKeys matérialise uniquement les clés satisfaisant les prédicats.
// Zéro allocation de valeurs, idéal pour indexation rapide ou listage de clés volumineuses.
func (q *Query) CollectKeys() ([][]byte, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.cursor == nil {
		return nil, unix.EBADF
	}
	if q.ownsCur {
		defer q.cursor.Close()
	}

	capEstimate := 16
	if q.limit > 0 && q.limit < capEstimate {
		capEstimate = q.limit
	}
	keys := make([][]byte, 0, capEstimate)

	err := q.iterate(false, false, func(k, _ []byte, _ io.Reader) bool {
		keys = append(keys, append([]byte(nil), k...))
		return true
	})
	return keys, err
}

// Scan itère en streaming continu sur les résultats correspondants sans allocation intermédiaire.
// Si fn retourne false, l'itération s'arrête immédiatement.
func (q *Query) Scan(fn func(key, val []byte) bool) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.cursor == nil {
		return unix.EBADF
	}
	if q.ownsCur {
		defer q.cursor.Close()
	}

	return q.iterate(true, false, func(k, v []byte, _ io.Reader) bool {
		return fn(k, v)
	})
}

// ScanKeys itère en streaming uniquement sur les clés sans résoudre ni allouer les valeurs.
func (q *Query) ScanKeys(fn func(key []byte) bool) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.cursor == nil {
		return unix.EBADF
	}
	if q.ownsCur {
		defer q.cursor.Close()
	}

	return q.iterate(false, false, func(k, _ []byte, _ io.Reader) bool {
		return fn(k)
	})
}

// Stream fournit un flux io.Reader pour chaque valeur correspondante, permettant
// de consommer de grands objets d'overflow sans matérialiser l'intégralité du blob en RAM.
func (q *Query) Stream(fn func(key []byte, r io.Reader) bool) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.cursor == nil {
		return unix.EBADF
	}
	if q.ownsCur {
		defer q.cursor.Close()
	}

	return q.iterate(false, true, func(k, _ []byte, r io.Reader) bool {
		return fn(k, r)
	})
}

// Project exécute un pipeline de projection à la volée : transforme chaque paire correspondante
// via mapper et transmet le résultat à consumer.
func (q *Query) Project(mapper func(key, val []byte) ([]byte, error), consumer func(projected []byte) bool) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.cursor == nil {
		return unix.EBADF
	}
	if q.ownsCur {
		defer q.cursor.Close()
	}

	return q.iterate(true, false, func(k, v []byte, _ io.Reader) bool {
		proj, err := mapper(k, v)
		if err != nil {
			return false
		}
		return consumer(proj)
	})
}

// iterate implémente le moteur d'exécution séquentiel avec refoulement des prédicats.
func (q *Query) iterate(resolveFullVal bool, needStream bool, yield func(k, v []byte, r io.Reader) bool) error {
	var k []byte
	var ok bool

	if !q.reverse {
		// Parcours ascendant
		if len(q.start) == 0 {
			k, _, ok = q.cursor.First()
		} else {
			k, _, ok = q.cursor.Seek(q.start)
		}
	} else {
		// Parcours descendant
		if len(q.end) == 0 {
			k, _, ok = q.cursor.Last()
		} else {
			k, _, ok = q.cursor.Seek(q.end)
			if !ok {
				k, _, ok = q.cursor.Last()
			} else if bytes.Compare(k, q.end) >= 0 {
				k, _, ok = q.cursor.Prev()
			}
		}
	}

	matched := 0
	yielded := 0

	for ok {
		// 1. Contrôle des bornes d'intervalle et préfixe
		if !q.reverse {
			if len(q.end) > 0 && bytes.Compare(k, q.end) >= 0 {
				break
			}
		} else {
			if len(q.start) > 0 && bytes.Compare(k, q.start) < 0 {
				break
			}
		}

		// 2. Refoulement de prédicats de clé (zéro-allocation)
		keyPassed := true
		for _, kp := range q.keyPreds {
			if !kp(k) {
				keyPassed = false
				break
			}
		}
		if !keyPassed {
			k, ok = q.advance()
			continue
		}

		// 3. Refoulement de prédicats de cellule brute (zéro déchiffrement / zéro lecture overflow)
		rawPassed := true
		rawVal := q.cursor.RawValue()
		for _, rp := range q.rawPreds {
			if !rp(rawVal) {
				rawPassed = false
				break
			}
		}
		if !rawPassed {
			k, ok = q.advance()
			continue
		}

		// 4. Évaluation des prédicats généraux sur valeur décodée si présents
		var v []byte
		if len(q.allPreds) > 0 || resolveFullVal {
			v = q.cursor.Value()
		}

		allPassed := true
		for _, ap := range q.allPreds {
			if !ap(k, v) {
				allPassed = false
				break
			}
		}
		if !allPassed {
			k, ok = q.advance()
			continue
		}

		// 5. Gestion de l'offset
		matched++
		if matched <= q.offset {
			k, ok = q.advance()
			continue
		}

		// 6. Émission du résultat
		var reader io.Reader
		if needStream {
			reader, _ = q.cursor.ValueReader()
		}

		if !yield(k, v, reader) {
			break
		}

		yielded++
		if q.limit > 0 && yielded >= q.limit {
			break
		}

		k, ok = q.advance()
	}

	return nil
}

func (q *Query) advance() ([]byte, bool) {
	if !q.reverse {
		k, _, ok := q.cursor.Next()
		return k, ok
	}
	k, _, ok := q.cursor.Prev()
	return k, ok
}

// prefixUpperBound calcule la borne supérieure stricte d'un préfixe d'octets.
// Renvoie nil si le préfixe est composé exclusivement de 0xFF (pas de borne supérieure).
func prefixUpperBound(prefix []byte) []byte {
	if len(prefix) == 0 {
		return nil
	}
	end := make([]byte, len(prefix))
	copy(end, prefix)
	for i := len(end) - 1; i >= 0; i-- {
		if end[i] < 0xFF {
			end[i]++
			return end[:i+1]
		}
	}
	return nil
}

// fastExtractJSONField extraite un champ chaîne d'un document JSON plat, sans allocation.
// Scanneur conscient de l'état chaîne : un match du nom de champ n'est retenu que s'il
// est en position de clé (suivi de ':' après espaces) et non à l'intérieur d'une valeur
// chaîne. Sound pour les objets plats ; un champ homonyme imbriqué dans un objet fils
// reste une limitation documentée (première occurrence en position de clé).
func fastExtractJSONField(json []byte, field string) (string, bool) {
	fBytes := []byte(`"` + field + `"`)
	n := len(json)
	i := 0
	inString := false
	escaped := false
	for i < n {
		c := json[i]
		if inString {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			i++
			continue
		}
		if c != '"' {
			i++
			continue
		}
		// Début d'une chaîne hors valeur : vérifier si c'est le champ recherché.
		if i+len(fBytes) <= n && bytes.Equal(json[i:i+len(fBytes)], fBytes) {
			j := i + len(fBytes)
			for j < n && (json[j] == ' ' || json[j] == '\t' || json[j] == '\r' || json[j] == '\n') {
				j++
			}
			if j < n && json[j] == ':' {
				j++
				for j < n && (json[j] == ' ' || json[j] == '\t' || json[j] == '\r' || json[j] == '\n') {
					j++
				}
				if j < n && json[j] == '"' {
					j++
					startVal := j
					esc := false
					for j < n {
						if esc {
							esc = false
							j++
							continue
						}
						if json[j] == '\\' {
							esc = true
							j++
							continue
						}
						if json[j] == '"' {
							return string(json[startVal:j]), true
						}
						j++
					}
				}
				return "", false
			}
			// Pas de ':' : c'est une valeur chaîne. Le match couvre la chaîne entière
			// (guillemet fermant inclus) : on la saute, l'état chaîne reste hors-chaîne.
			i = i + len(fBytes)
			continue
		}
		inString = true
		i++
	}
	return "", false
}
