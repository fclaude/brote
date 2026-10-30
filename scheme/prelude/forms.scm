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
;; that yields several packs them into a list. Every expr is evaluated before
;; any name is bound, in the scope around the form, as R7RS has it:
;; %let-values evaluates each into a temporary (a let binder, so each
;; expansion's is fresh), then %let-values-bind binds the formals.
(define-syntax let-values
  (syntax-rules ()
    ((let-values (binding ...) body ...)
     (%let-values (binding ...) () body ...))))

(define-syntax %let-values
  (syntax-rules ()
    ((%let-values () (bound ...) body ...)
     (%let-values-bind (bound ...) body ...))
    ((%let-values ((formals expr) binding ...) (bound ...) body ...)
     (let ((tmp expr))
       (%let-values (binding ...) (bound ... (formals tmp)) body ...)))))

(define-syntax %let-values-bind
  (syntax-rules ()
    ((%let-values-bind () body ...)
     (let () body ...))
    ((%let-values-bind ((formals tmp) bound ...) body ...)
     (apply (lambda formals (%let-values-bind (bound ...) body ...)) tmp))))

(define-syntax case-lambda
  (syntax-rules ()
    ((case-lambda (formals body ...) ...)
     (lambda args
       (let ((n (length args)))
         (cond
           ((%case-lambda-fits? 'formals n) (apply (lambda formals body ...) args))
           ...
           (else (error "case-lambda: no clause matches" args))))))))

;; Records are vectors: #(tag field-names value …). The tag is a pair made
;; once for each define-record-type form, held in a let around the
;; procedures, so the predicate is true only for instances made by that form's
;; constructor — not for another type that has the same name, and not for one
;; a later form of the same name defines. The procedures are defined first
;; and set inside that let, since a define inside it would be local. The
;; names of the constructor, predicate, accessors and mutators are written in
;; the form: syntax-rules cannot join a type name and a field name into one.
;; A field spec is (field accessor) or (field accessor mutator).

(define %record-index
  (lambda (fields f)
    "The position of f in fields, counting from 0."
    (let loop ((l fields) (k 0))
      (cond ((null? l) (error "define-record-type: no such field" f))
            ((eq? (car l) f) k)
            (else (loop (cdr l) (+ k 1)))))))

(define-syntax define-record-type
  (syntax-rules ()
    ((define-record-type type (ctor field ...) pred spec ...)
     (begin
       (define ctor #f)
       (define pred #f)
       (%record-define spec) ...
       (let ((tag (list 'type)))
         (set! ctor (lambda (field ...) (vector tag (quote (field ...)) field ...)))
         (set! pred (lambda (v)
                      (and (vector? v)
                           (>= (vector-length v) 2)
                           (eq? (vector-ref v 0) tag))))
         (%record-set type pred spec) ...
         (void))))))

(define-syntax %record-define
  (syntax-rules ()
    ((%record-define (fname acc)) (define acc #f))
    ((%record-define (fname acc mut)) (begin (define acc #f) (define mut #f)))))

(define-syntax %record-set
  (syntax-rules ()
    ((%record-set type pred (fname acc))
     (set! acc (lambda (r)
                 (if (pred r)
                     (vector-ref r (+ 2 (%record-index (vector-ref r 1) 'fname)))
                     (error (string-append "not a " (symbol->string 'type) " record") r)))))
    ((%record-set type pred (fname acc mut))
     (begin
       (%record-set type pred (fname acc))
       (set! mut (lambda (r v)
                   (if (pred r)
                       (begin (vector-set! r (+ 2 (%record-index (vector-ref r 1) 'fname)) v) (void))
                       (error (string-append "not a " (symbol->string 'type) " record") r))))))))
