All credit to this blog which was utilized: https://mikeluttikhuis.nl/2024/11/exploring-mutating-webhooks-in-kubernetes/

The only thing I did was organize the information into seperate files and create a script for easy to deployment, I also modified some of the code logic to fit my own purpose. The end goal was to create a mutating webhook that would take 2 different pod labels and concatenate them in order to form a new label. 

Notes:
1. All of the code logic can be found in the controller.go file

Everything below is a work in progress:

Prerequisities 
- minikube
- kubectl
- go

Step by step guide:

Certificate Manager 
1. Run the following command in terminal kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.16.1/cert-manager.yaml, this will be the cert manager needed to interact with the kubernetes API
2. Create the necessary certificates by navigating to cert-manager and running kubectl apply -f cert-manager.yaml, this will create a namespace, ClusterIssuer, Certificates, Issuer

Creating RBAC and Webhook Deployment
1. Navigate to the admission-controller folder and make sure you have execute permissions on the deploy.sh file, if you do not currently have execute permissions you can run the following chmod +x deploy.sh
2. From here you can run ./deploy.sh in your terminal, this will take care of deploying the RBAC and the Mutating Webhook

Testing the Mutating Webhook
1. Run kubectl get pods -A, if everything is deployed successfully within minikube you should see something similar to the below:

NAMESPACE              NAME                                       READY   STATUS    RESTARTS      AGE
admission-controller   mutating-webhook-5d4bc9d94d-dfnhz          1/1     Running   0             13m
cert-manager           cert-manager-5c887c889d-stp4l              1/1     Running   1 (69m ago)   74m
cert-manager           cert-manager-cainjector-58f6855565-x5v9z   1/1     Running   1 (70m ago)   74m
cert-manager           cert-manager-webhook-6647d6545d-76wfz      1/1     Running   1 (70m ago)   74m
kube-system            coredns-6f6b679f8f-zwjnx                   1/1     Running   1 (70m ago)   78m
kube-system            etcd-us-bank-webhook                       1/1     Running   1 (70m ago)   78m
kube-system            kube-apiserver-us-bank-webhook             1/1     Running   1 (69m ago)   78m
kube-system            kube-controller-manager-us-bank-webhook    1/1     Running   1 (70m ago)   78m
kube-system            kube-proxy-zlgcx                           1/1     Running   1 (70m ago)   78m
kube-system            kube-scheduler-us-bank-webhook             1/1     Running   1 (70m ago)   78m
kube-system            storage-provisioner                        1/1     Running   8 (21m ago)   78m

2. From here you can test the webhook by navigating to nginx folder and running kubectl create -f nginx-test-pod.yaml, you should see a new pod spin up in the default namespace called nginx-XXXX, since the pod labels on the nginx-test-pod.yaml file are as follows

  labels:
    appName: nginx
    car_id: "01234"

We should expect the webhook to take the appName and the car_id and create a new label within the pod called service, in this case the pod will have the following service label after creation 

Name:             nginx-nvcwq
Namespace:        default
Priority:         0
Service Account:  default
Node:             us-bank-webhook/192.168.76.2
Start Time:       Tue, 26 Aug 2025 17:02:37 -0700
Labels:           appName=nginx
                  car_id=01234
                  service=nginx-01234
Annotations:      <none>
Status:           Running
IP:               10.244.0.27
IPs:
  IP:  10.244.0.27
Containers:
  nginx:
    Container ID:   docker://f30054850fa1ca71d46edc3a769d6ddf2611711b14f048eeae8a307be0d11422
    Image:          nginx:1.14.2
    Image ID:       docker-pullable://nginx@sha256:f7988fb6c02e0ce69257d9bd9cf37ae20a60f1df7563c3a2a6abe24160306b8d
    Port:           80/TCP
    Host Port:      0/TCP
    State:          Running
      Started:      Tue, 26 Aug 2025 17:02:37 -0700
    Ready:          True
    Restart Count:  0
    Environment:    <none>
    Mounts:
      /var/run/secrets/kubernetes.io/serviceaccount from kube-api-access-7gdx4 (ro)
Conditions:
  Type                        Status
  PodReadyToStartContainers   True
  Initialized                 True
  Ready                       True
  ContainersReady             True
  PodScheduled                True
Volumes:
  kube-api-access-7gdx4:
    Type:                    Projected (a volume that contains injected data from multiple sources)
    TokenExpirationSeconds:  3607
    ConfigMapName:           kube-root-ca.crt
    ConfigMapOptional:       <nil>
    DownwardAPI:             true
QoS Class:                   BestEffort
Node-Selectors:              <none>
Tolerations:                 node.kubernetes.io/not-ready:NoExecute op=Exists for 300s
                             node.kubernetes.io/unreachable:NoExecute op=Exists for 300s
Events:
  Type    Reason     Age   From               Message
  ----    ------     ----  ----               -------
  Normal  Scheduled  119s  default-scheduler  Successfully assigned default/nginx-nvcwq to us-bank-webhook
  Normal  Pulled     2m    kubelet            Container image "nginx:1.14.2" already present on machine
  Normal  Created    2m    kubelet            Created container nginx
  Normal  Started    2m    kubelet            Started container nginx


To do:
- fix readme.md
- clean up code and explain more logic with inline comments