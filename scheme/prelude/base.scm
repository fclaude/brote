;; Brote prelude: loaded into the global environment before anything else.
;; Documented in docs/scheme.md §6 and docs/stdlib.md. Keep it small: anything
;; here is part of the language contract.

(define (assoc-ref alist key . default)
  "The value paired with key in alist, or the default (void if none is given)."
  (let ((hit (assoc key alist)))
    (cond (hit (cdr hit))
          ((null? default) (void))
          (else (car default)))))

(define (identity x)
  "Return x."
  x)

(define (compose f g)
  "A procedure that applies g, then f to its result."
  (lambda args (f (apply g args))))

(define (string-empty? s)
  "True if s has no characters."
  (= 0 (string-length s)))
