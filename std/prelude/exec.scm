;; std exec prelude: conveniences over proc-run. Documented in docs/std.md.

(define (proc-output argv . options)
  (let ((r (apply proc-run argv options)))
    (if (= (assoc-ref r "exit-code") 0)
        (assoc-ref r "stdout")
        (error (string-append (car argv) " exited with status "
                              (number->string (assoc-ref r "exit-code"))
                              ": " (assoc-ref r "stderr"))))))
