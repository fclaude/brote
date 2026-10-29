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
