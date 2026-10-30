;; Binding forms written in Scheme: let-values and case-lambda.
;; Documented in docs/scheme.md §6.

(define %case-lambda-fits?
  (lambda (formals n)
    "Whether a procedure with these formals takes n arguments."
    (let loop ((f formals) (k 0))
      (cond ((pair? f) (loop (cdr f) (+ k 1)))
            ((null? f) (= k n))
            (else (>= n k))))))

;; Each producer's value must be a list with one element per name in its
;; formals: the eval loop returns one value per expression, so a producer
;; that yields several packs them into a list.
(define-syntax let-values
  (syntax-rules ()
    ((let-values () body ...)
     (begin body ...))
    ((let-values ((formals expr) binding ...) body ...)
     (let ((tmp expr))
       (apply (lambda formals (let-values (binding ...) body ...)) tmp)))))

(define-syntax case-lambda
  (syntax-rules ()
    ((case-lambda (formals body ...) ...)
     (lambda args
       (let ((n (length args)))
         (cond
           ((%case-lambda-fits? 'formals n) (apply (lambda formals body ...) args))
           ...
           (else (error "case-lambda: no clause matches" args))))))))
