;; Promises: (delay expr) evaluates expr on the first force only.
;; Documented in docs/scheme.md §5 (Tasks) and §6 (Prelude).

(define-syntax delay
  (syntax-rules ()
    ((delay expr) (make-promise (lambda () expr)))))
