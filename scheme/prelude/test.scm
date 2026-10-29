;; Testing: test, assert, assert-equal, assert-error. Documented in
;; docs/stdlib.md. `brote test` runs files of tests and reports test-results.

(define %tests '())

(define %condition-text
  (lambda (e)
    "The message of a condition and its irritants, as an error prints them."
    (apply string-append (assoc-ref e 'message "")
           (map (lambda (x)
                  (string-append " " (call-with-output-string (lambda (p) (write x p)))))
                (assoc-ref e 'irritants '())))))

(define (run-test name thunk)
  "Call thunk and record whether it returned or raised an error, under name."
  (set! %tests
        (cons (guard (e (#t (list name #f (%condition-text e)
                                  (assoc-ref e 'where #f)
                                  (assoc-ref e 'trace '()))))
                (thunk)
                (list name #t "" #f '()))
              %tests))
  (void))

(define (test-results)
  "What the tests so far did, oldest first: a list of (name passed? message where trace)."
  (reverse %tests))

(define-syntax test
  (syntax-rules ()
    ((_ name body ...) (run-test name (lambda () body ...)))))

(define-syntax assert
  (syntax-rules ()
    ((_ expr) (if expr #t (error "assertion failed:" 'expr)))
    ((_ expr message) (if expr #t (error message 'expr)))))

(define-syntax assert-equal
  (syntax-rules ()
    ((_ expected expr)
     (let ((want expected) (got expr))
       (if (equal? want got)
           #t
           (error "assert-equal failed:" 'expr "expected" want "got" got))))))

(define-syntax assert-error
  (syntax-rules ()
    ((_ expr)
     (if (guard (e (#t #f)) expr #t)
         (error "assert-error: no error from" 'expr)
         #t))
    ((_ expr text)
     (if (guard (e (#t (string-contains? (%condition-text e) text))) expr #f)
         #t
         (error "assert-error: wanted an error that mentions" text 'expr)))))
