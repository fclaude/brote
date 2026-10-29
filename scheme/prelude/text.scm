;; String procedures written in Scheme. Documented in docs/stdlib.md.

(define (string-index s sub)
  "The index of the first place sub appears in s, or #f."
  (let ((n (string-length s)) (m (string-length sub)))
    (let loop ((i 0))
      (cond ((> (+ i m) n) #f)
            ((string=? (substring s i (+ i m)) sub) i)
            (else (loop (+ i 1)))))))

(define (string-pad-left s n . pad)
  "Add pad (a space unless given) on the left of s until it is n characters long."
  (let ((p (if (null? pad) " " (car pad))))
    (let loop ((s s))
      (if (>= (string-length s) n) s (loop (string-append p s))))))

(define (string-pad-right s n . pad)
  "Add pad (a space unless given) on the right of s until it is n characters long."
  (let ((p (if (null? pad) " " (car pad))))
    (let loop ((s s))
      (if (>= (string-length s) n) s (loop (string-append s p))))))
