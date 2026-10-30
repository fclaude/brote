;; The language's own behaviour, written as Brote tests. `brote test tests`
;; runs this file; so does `go test ./cmd/brote`.

(test "tail calls run in constant space"
  (define (count n acc) (if (= n 0) acc (count (- n 1) (+ acc 1))))
  (assert-equal 100000 (count 100000 0)))

(test "guard catches an error and reads its condition"
  (assert-equal '("boom" (1 2))
                (guard (e (#t (list (assoc-ref e 'message) (assoc-ref e 'irritants))))
                  (error "boom" 1 2))))

(test "an error says where it was raised"
  (assert (string? (guard (e (#t (assoc-ref e 'where))) (car 5)))))

(test "macros: swap! does not capture the caller's tmp"
  (define-syntax swap!
    (syntax-rules () ((_ a b) (let ((tmp a)) (set! a b) (set! b tmp)))))
  (define tmp 1)
  (define other 2)
  (swap! tmp other)
  (assert-equal '(2 1) (list tmp other)))

(test "macros: ellipsis in the middle of a pattern"
  (define-syntax last-of (syntax-rules () ((_ a ... z) 'z)))
  (assert-equal 3 (last-of 1 2 3)))

(test "quasiquote nests"
  (assert-equal '(a (quasiquote (b (unquote (c 3))))) `(a `(b ,(c ,(+ 1 2))))))

(test "characters and strings"
  (assert-equal #\é (string-ref "héllo" 1))
  (assert-equal "olleh" (list->string (reverse (string->list "hello"))))
  (assert-equal 955 (char->integer #\λ)))

(test "string ports"
  (define in (open-input-string "one\ntwo (3 4)"))
  (assert-equal "one" (read-line in))
  (assert-equal 'two (read in))
  (assert-equal '(3 4) (read in))
  (assert (eof-object? (read in)))
  (assert-equal "a1" (call-with-output-string (lambda (p) (write-char #\a p) (write 1 p)))))

(test "pairs can be changed by a program running alone"
  (define l (list 1 2 3))
  (set-car! (cdr l) 'x)
  (assert-equal '(1 x 3) l)
  (assert-error (set-cdr! (cddr l) l) "circular"))

(test "tasks run at the same time and return their values"
  (assert-equal '(1 4 9) (map task-wait (map (lambda (n) (spawn (lambda () (* n n)))) '(1 2 3)))))

(test "doc describes a procedure written in Scheme"
  (define (double x) "Twice x." (* 2 x))
  (assert-equal "(double x)\nTwice x." (doc double)))

(test "let-values binds each producer's list of values"
  (assert-equal 6 (let-values (((a b) (list 1 2)) ((c) (list 3))) (+ a b c)))
  (assert-equal '(1 2 3) (let-values (((x y z) (list 1 2 3))) (list x y z)))
  (assert-equal '(1 2 3) (let-values (((x . rest) (list 1 2 3))) (cons x rest)))
  ;; Every producer is evaluated before any name is bound, in the outer scope.
  (assert-equal '(10 1) (let ((x 1)) (let-values (((x) (list 10)) ((y) (list x))) (list x y))))
  (assert-equal 5 (let-values () 5)))

(test "case-lambda picks the clause whose formals fit"
  (define f (case-lambda ((x) (* x 10)) ((x y) (+ x y))))
  (assert-equal 50 (f 5))
  (assert-equal 12 (f 5 7))
  (assert-error (f) "no clause")
  (assert-error (f 1 2 3) "no clause")
  (define g (case-lambda ((x . rest) (cons x rest)) (() 'none)))
  (assert-equal '(1 2 3) (g 1 2 3))
  (assert-equal 'none (g)))

(test "define-record-type makes a tagged vector with named accessors"
  (define-record-type point (make-point x y) point?
    (x point-x point-x-set!)
    (y point-y point-y-set!))
  (define-record-type line (make-line a b) line?
    (a line-a line-a-set!)
    (b line-b line-b-set!))
  (define p (make-point 1 2))
  (assert (point? p))
  (assert (not (point? #(1 2))))
  (assert (not (point? (make-line 1 2))))
  (assert (not (line? p)))
  (assert-equal 1 (point-x p))
  (assert-equal 2 (point-y p))
  (point-x-set! p 10)
  (assert-equal 10 (point-x p))
  (assert-equal 2 (point-y p))
  (assert-error (point-x #(0 1 2)) "not a")
  (assert-error (point-x-set! (make-line 1 2) 5) "not a"))

(test "define-record-type types are distinct even with the same name"
  (define-record-type cell (make-cell v) cell? (v cell-v))
  (define old (make-cell 1))
  (assert-equal 1 (cell-v old))
  ;; A second type named cell, with other fields: the old instance is not one.
  (define-record-type cell (make-cell a b) cell? (a cell-a cell-a-set!) (b cell-b))
  (assert (not (cell? old)))
  (assert (cell? (make-cell 1 2)))
  (assert-error (cell-a old) "not a"))