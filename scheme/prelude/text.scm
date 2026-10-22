;; String procedures written in Scheme. Documented in docs/stdlib.md.

(define (string-index s sub)
  (let ((n (string-length s)) (m (string-length sub)))
    (let loop ((i 0))
      (cond ((> (+ i m) n) #f)
            ((string=? (substring s i (+ i m)) sub) i)
            (else (loop (+ i 1)))))))

(define (string-pad-left s n . pad)
  (let ((p (if (null? pad) " " (car pad))))
    (let loop ((s s))
      (if (>= (string-length s) n) s (loop (string-append p s))))))

(define (string-pad-right s n . pad)
  (let ((p (if (null? pad) " " (car pad))))
    (let loop ((s s))
      (if (>= (string-length s) n) s (loop (string-append s p))))))
