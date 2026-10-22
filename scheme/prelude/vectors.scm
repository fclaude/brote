;; Vector procedures written in Scheme. Documented in docs/stdlib.md.

(define (vector-map f v)
  (list->vector (map f (vector->list v))))

(define (vector-for-each f v)
  (for-each f (vector->list v)))
