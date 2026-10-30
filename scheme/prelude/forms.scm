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

;; Records are vectors: #(tag field-names value …), where the tag is a fresh
;; gensym kept in a registry keyed by the type name, so the predicate is true
;; only for instances of this one type. The names of the constructor,
;; predicate, accessors and mutators are written in the form: syntax-rules
;; cannot join a type name and a field name into one.

(define %record-tags (make-hash))

(define %record-tag
  (lambda (type)
    "The unique tag of a record type: a gensym, made on first use and kept."
    (let ((t (hash-ref %record-tags type #f)))
      (if t
          t
          (let ((n (gensym (symbol->string type))))
            (hash-set! %record-tags type n)
            (hash-ref %record-tags type n))))))

(define %record-index
  (lambda (fields f)
    "The position of f in fields, counting from 0."
    (let loop ((l fields) (k 0))
      (cond ((null? l) (error "define-record-type: no such field" f))
            ((eq? (car l) f) k)
            (else (loop (cdr l) (+ k 1)))))))

(define-syntax define-record-type
  (syntax-rules ()
    ((define-record-type type (ctor field ...) pred (fname acc mut) ...)
     (begin
       (define (ctor field ...) (vector (%record-tag 'type) (quote (field ...)) field ...))
       (define (pred v)
         (and (vector? v)
              (>= (vector-length v) 2)
              (eq? (vector-ref v 0) (%record-tag 'type))
              (= (vector-length v) (+ 2 (length (vector-ref v 1))))))
       (define (acc r)
         (if (pred r)
             (vector-ref r (+ 2 (%record-index (vector-ref r 1) 'fname)))
             (error (string-append "not a " (symbol->string 'type) " record") r)))
       ...
       (define (mut r v)
         (if (pred r)
             (begin (vector-set! r (+ 2 (%record-index (vector-ref r 1) 'fname)) v) (void))
             (error (string-append "not a " (symbol->string 'type) " record") r)))
       ...))))
