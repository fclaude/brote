;; Vector procedures written in Scheme. Documented in docs/stdlib.md.

(define (vector-map f v)
  "A vector of (f x) for the elements of v."
  (list->vector (map f (vector->list v))))

(define (vector-for-each f v)
  "Call f on each element of v, in order."
  (for-each f (vector->list v)))
