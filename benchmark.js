import http from 'k6/http'
import { check, sleep } from 'k6'

export default function () {
//   const data = { username: 'username', password: 'password' }
  let res = http.post('http://localhost:80/pessoas')

  check(res, { 'success login': (r) => r.status === 200 })
}

export const options = {
    stages: [
        { duration: '1s', target: 100 },
        { duration: '5s', target: 500 },
        { duration: '30s', target: 1000 },
      ],
}