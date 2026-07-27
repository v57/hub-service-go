package hubservice

import "time"

// authTokenValidFor matches the Bun hub-service sign() default of 10 seconds.
const authTokenValidFor = 10 * time.Second

// networkRepublishTimeout bounds the on-reconnect share republication call.
const networkRepublishTimeout = 30 * time.Second
