;; List procedures written in Scheme. Documented in docs/stdlib.md.

(define (first l)
  "The first element of l."
  (car l))
(define (second l)
  "The second element of l."
  (cadr l))
(define (third l)
  "The third element of l."
  (caddr l))
(define (rest l)
  "All of l but the first element."
  (cdr l))

(define (take l n)
  "The first n elements of l."
  (let loop ((l l) (k n) (acc '()))
    (cond ((= k 0) (reverse acc))
          ((null? l) (error "take: the list is shorter than" n))
          (else (loop (cdr l) (- k 1) (cons (car l) acc))))))

(define (drop l n)
  "All of l but the first n elements."
  (let loop ((l l) (k n))
    (cond ((= k 0) l)
          ((null? l) (error "drop: the list is shorter than" n))
          (else (loop (cdr l) (- k 1))))))

(define (last l)
  "The last element of l."
  (cond ((null? l) (error "last: the list is empty"))
        ((null? (cdr l)) (car l))
        (else (last (cdr l)))))

(define (any pred l)
  "The first true value of (pred x) for the elements of l, or #f."
  (let loop ((l l))
    (if (null? l)
        #f
        (or (pred (car l)) (loop (cdr l))))))

(define (every pred l)
  "The last value of (pred x) if it is true for every element of l, else #f."
  (let loop ((l l) (result #t))
    (if (null? l)
        result
        (let ((r (pred (car l))))
          (if r (loop (cdr l) r) #f)))))

(define (count pred l)
  "How many elements of l satisfy pred."
  (fold (lambda (n x) (if (pred x) (+ n 1) n)) 0 l))

(define (remove pred l)
  "The elements of l that do not satisfy pred."
  (filter (lambda (x) (not (pred x))) l))

(define (delete x l)
  "The elements of l that are not equal? to x."
  (remove (lambda (y) (equal? x y)) l))

(define (delete-duplicates l)
  "l without repeated elements; the first of each stays."
  (let loop ((l l) (seen '()))
    (cond ((null? l) (reverse seen))
          ((member (car l) seen) (loop (cdr l) seen))
          (else (loop (cdr l) (cons (car l) seen))))))

(define (list-index pred l)
  "The index of the first element of l that satisfies pred, or #f."
  (let loop ((l l) (i 0))
    (cond ((null? l) #f)
          ((pred (car l)) i)
          (else (loop (cdr l) (+ i 1))))))

(define (partition pred l)
  "A list of two lists: the elements that satisfy pred, and the rest."
  (list (filter pred l) (remove pred l)))

(define (filter-map f l)
  "The true values of (f x) for the elements of l."
  (let loop ((l l) (acc '()))
    (if (null? l)
        (reverse acc)
        (let ((r (f (car l))))
          (loop (cdr l) (if r (cons r acc) acc))))))

(define (append-map f l)
  "The lists (f x) for the elements of l, appended."
  (apply append (map f l)))
