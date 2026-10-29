;; std net prelude: conveniences over http-request. Documented in docs/std.md.
;; Only the documented names are defined at the top level.

(define (http-get url . headers)
  (http-request "GET" url (if (null? headers) '() (car headers))))

(define (http-post url body . headers)
  (http-request "POST" url (if (null? headers) '() (car headers)) body))

(define http-get-json
  (lambda (url . headers)
    (let* ((response (apply http-get url headers))
           (status (assoc-ref response "status")))
      (if (>= status 400)
          (error (string-append "http-get-json: " url " answered HTTP " (number->string status)))
          (json-parse (assoc-ref response "body"))))))

(define http-post-json
  (lambda (url value . headers)
    (let* ((response (http-request "POST" url
                                   (cons '("content-type" . "application/json")
                                         (if (null? headers) '() (car headers)))
                                   (json-emit value)))
           (status (assoc-ref response "status")))
      (if (>= status 400)
          (error (string-append "http-post-json: " url " answered HTTP " (number->string status)))
          (json-parse (assoc-ref response "body"))))))
