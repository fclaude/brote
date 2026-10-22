;; List procedures written in Scheme. Documented in docs/stdlib.md.

(define (first l) (car l))
(define (second l) (cadr l))
(define (third l) (caddr l))
(define (rest l) (cdr l))

(define (take l n)
  (let loop ((l l) (k n) (acc '()))
    (cond ((= k 0) (reverse acc))
          ((null? l) (error "take: the list is shorter than" n))
          (else (loop (cdr l) (- k 1) (cons (car l) acc))))))

(define (drop l n)
  (let loop ((l l) (k n))
    (cond ((= k 0) l)
          ((null? l) (error "drop: the list is shorter than" n))
          (else (loop (cdr l) (- k 1))))))

(define (last l)
  (cond ((null? l) (error "last: the list is empty"))
        ((null? (cdr l)) (car l))
        (else (last (cdr l)))))

(define (any pred l)
  (let loop ((l l))
    (if (null? l)
        #f
        (or (pred (car l)) (loop (cdr l))))))

(define (every pred l)
  (let loop ((l l) (result #t))
    (if (null? l)
        result
        (let ((r (pred (car l))))
          (if r (loop (cdr l) r) #f)))))

(define (count pred l)
  (fold (lambda (n x) (if (pred x) (+ n 1) n)) 0 l))

(define (remove pred l)
  (filter (lambda (x) (not (pred x))) l))

(define (delete x l)
  (remove (lambda (y) (equal? x y)) l))

(define (delete-duplicates l)
  (let loop ((l l) (seen '()))
    (cond ((null? l) (reverse seen))
          ((member (car l) seen) (loop (cdr l) seen))
          (else (loop (cdr l) (cons (car l) seen))))))

(define (list-index pred l)
  (let loop ((l l) (i 0))
    (cond ((null? l) #f)
          ((pred (car l)) i)
          (else (loop (cdr l) (+ i 1))))))

(define (partition pred l)
  (list (filter pred l) (remove pred l)))

(define (filter-map f l)
  (let loop ((l l) (acc '()))
    (if (null? l)
        (reverse acc)
        (let ((r (f (car l))))
          (loop (cdr l) (if r (cons r acc) acc))))))

(define (append-map f l)
  (apply append (map f l)))
